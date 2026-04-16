interface Env {
  WAKEUP_KV: KVNamespace;
  AUTH_TOKEN: string;
}

interface WakeSignal {
  wake: boolean;
  duration: number;
  created_at: number;
}

interface DeviceInfo {
  last_seen: number;
}

// ConfigData contains remotely manageable fields (excludes worker_url, token, device_id).
interface ConfigData {
  check_interval?: number; // seconds
  default_duration?: number; // seconds
  ac_check_interval?: number; // seconds
  battery_check_interval?: number; // seconds
  enable_darkwake_detection?: boolean;
  wake_detect_interval?: number; // seconds
}

function jsonResponse(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function errorResponse(message: string, status: number): Response {
  return jsonResponse({ error: message }, status);
}

// Strip fields that must not be stored remotely.
function sanitizeConfig(data: Record<string, unknown>): ConfigData {
  const allowed = [
    "check_interval",
    "default_duration",
    "ac_check_interval",
    "battery_check_interval",
    "enable_darkwake_detection",
    "wake_detect_interval",
  ];
  const result: Record<string, unknown> = {};
  for (const key of allowed) {
    if (key in data) {
      result[key] = data[key];
    }
  }
  return result as ConfigData;
}

// Validate config field constraints. Returns error message or null.
function validateConfig(cfg: ConfigData): string | null {
  const minInterval = 60; // 1 minute in seconds
  const minWakeDetect = 10; // 10 seconds
  if (cfg.check_interval !== undefined && cfg.check_interval < minInterval) {
    return "check_interval must be at least 60s";
  }
  if (
    cfg.default_duration !== undefined &&
    cfg.default_duration < minInterval
  ) {
    return "default_duration must be at least 60s";
  }
  if (
    cfg.ac_check_interval !== undefined &&
    cfg.ac_check_interval < minInterval
  ) {
    return "ac_check_interval must be at least 60s";
  }
  if (
    cfg.battery_check_interval !== undefined &&
    cfg.battery_check_interval < minInterval
  ) {
    return "battery_check_interval must be at least 60s";
  }
  if (
    cfg.wake_detect_interval !== undefined &&
    cfg.wake_detect_interval < minWakeDetect
  ) {
    return "wake_detect_interval must be at least 10s";
  }
  return null;
}

// Compute a hex-encoded hash of the config JSON for version comparison.
async function configVersionHash(cfg: ConfigData): Promise<string> {
  const data = new TextEncoder().encode(JSON.stringify(cfg));
  const hash = await crypto.subtle.digest("MD5", data);
  return Array.from(new Uint8Array(hash))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

// Merge global and device-level configs. Device-level fields override global.
function mergeConfigs(
  global: ConfigData | null,
  device: ConfigData | null,
): ConfigData {
  const merged: ConfigData = {};
  if (global) Object.assign(merged, global);
  if (device) {
    // Only override with explicitly set fields (not undefined)
    for (const [key, value] of Object.entries(device)) {
      if (value !== undefined) {
        (merged as Record<string, unknown>)[key] = value;
      }
    }
  }
  return merged;
}

// Get merged config and version hash for a device.
async function getMergedConfig(
  env: Env,
  deviceId: string,
): Promise<{ config: ConfigData; version: string } | null> {
  const globalRaw = await env.WAKEUP_KV.get("config:global");
  const deviceRaw = await env.WAKEUP_KV.get(`config:device:${deviceId}`);

  if (!globalRaw && !deviceRaw) return null;

  const global: ConfigData | null = globalRaw ? JSON.parse(globalRaw) : null;
  const device: ConfigData | null = deviceRaw ? JSON.parse(deviceRaw) : null;
  const merged = mergeConfigs(global, device);
  const version = await configVersionHash(merged);
  return { config: merged, version };
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);
    const parts = url.pathname.split("/").filter(Boolean);

    // Validate token: first path segment must match AUTH_TOKEN
    if (parts.length < 1 || parts[0] !== env.AUTH_TOKEN) {
      return errorResponse("not found", 404);
    }

    const action = parts[1];
    const deviceId = parts[2];

    // Route: POST /{token}/wake/{device_id}
    // Route: POST /{token}/wake?all=true
    if (action === "wake" && request.method === "POST") {
      let duration = 1800; // default 30 minutes
      try {
        const body = await request.json<{ duration?: number }>();
        if (body.duration && body.duration > 0) {
          duration = body.duration;
        }
      } catch {
        // no body or invalid json, use defaults
      }

      const all = url.searchParams.get("all") === "true";

      if (all) {
        // Broadcast to all registered devices
        const deviceKeys = await env.WAKEUP_KV.list({ prefix: "device:" });
        const ids = deviceKeys.keys.map((k) => k.name.replace("device:", ""));
        for (const id of ids) {
          const signal: WakeSignal = {
            wake: true,
            duration,
            created_at: Date.now(),
          };
          await env.WAKEUP_KV.put(
            `wake-signal:${id}`,
            JSON.stringify(signal),
            {
              expirationTtl: 86400, // 1 day
            },
          );
        }
        return jsonResponse({ ok: true, devices: ids, duration });
      }

      if (!deviceId) {
        return errorResponse("device_id required", 400);
      }

      const device = await env.WAKEUP_KV.get(`device:${deviceId}`);
      if (!device) {
        return errorResponse("device not found", 404);
      }

      const signal: WakeSignal = {
        wake: true,
        duration,
        created_at: Date.now(),
      };
      await env.WAKEUP_KV.put(
        `wake-signal:${deviceId}`,
        JSON.stringify(signal),
        { expirationTtl: 86400 },
      );
      return jsonResponse({ ok: true, device: deviceId, duration });
    }

    // Route: GET /{token}/check/{device_id}
    if (action === "check" && request.method === "GET") {
      if (!deviceId) {
        return errorResponse("device_id required", 400);
      }

      // Update device last_seen
      const deviceInfo: DeviceInfo = { last_seen: Date.now() };
      await env.WAKEUP_KV.put(
        `device:${deviceId}`,
        JSON.stringify(deviceInfo),
      );

      // Read and delete signal
      const signalKey = `wake-signal:${deviceId}`;
      const signalRaw = await env.WAKEUP_KV.get(signalKey);

      // Build base response
      let response: Record<string, unknown>;
      if (signalRaw) {
        await env.WAKEUP_KV.delete(signalKey);
        response = JSON.parse(signalRaw);
      } else {
        response = { wake: false };
      }

      // Piggyback config if available and changed
      const clientCV = url.searchParams.get("cv");
      const merged = await getMergedConfig(env, deviceId);
      if (merged) {
        if (!clientCV || clientCV !== merged.version) {
          response.config = merged.config;
          response.config_version = merged.version;
        }
      }

      return jsonResponse(response);
    }

    // Route: PUT /{token}/config — set global config
    // Route: GET /{token}/config — get global config
    // Route: PUT /{token}/config/{device_id} — set device config
    // Route: GET /{token}/config/{device_id} — get device config
    // Route: DELETE /{token}/config/{device_id} — delete device config
    if (action === "config") {
      if (request.method === "PUT") {
        let body: Record<string, unknown>;
        try {
          body = await request.json<Record<string, unknown>>();
        } catch {
          return errorResponse("invalid JSON body", 400);
        }
        if (!body || Object.keys(body).length === 0) {
          return errorResponse("empty config body", 400);
        }

        const cfg = sanitizeConfig(body);
        const err = validateConfig(cfg);
        if (err) {
          return errorResponse(err, 400);
        }

        const key = deviceId
          ? `config:device:${deviceId}`
          : "config:global";
        await env.WAKEUP_KV.put(key, JSON.stringify(cfg));

        const version = await configVersionHash(cfg);
        return jsonResponse({ ok: true, config: cfg, version });
      }

      if (request.method === "GET") {
        const key = deviceId
          ? `config:device:${deviceId}`
          : "config:global";
        const raw = await env.WAKEUP_KV.get(key);
        if (!raw) {
          return errorResponse("config not found", 404);
        }
        const cfg: ConfigData = JSON.parse(raw);
        const version = await configVersionHash(cfg);
        return jsonResponse({ config: cfg, version });
      }

      if (request.method === "DELETE") {
        if (!deviceId) {
          return errorResponse("device_id required for delete", 400);
        }
        const key = `config:device:${deviceId}`;
        const raw = await env.WAKEUP_KV.get(key);
        if (!raw) {
          return errorResponse("config not found", 404);
        }
        await env.WAKEUP_KV.delete(key);
        return jsonResponse({ ok: true });
      }

      return errorResponse("method not allowed", 405);
    }

    // Route: GET /{token}/status
    if (action === "status" && request.method === "GET") {
      const deviceKeys = await env.WAKEUP_KV.list({ prefix: "device:" });
      const statuses: Record<
        string,
        { last_seen: number; pending_wake: boolean }
      > = {};

      for (const k of deviceKeys.keys) {
        const id = k.name.replace("device:", "");
        const deviceRaw = await env.WAKEUP_KV.get(k.name);
        const device: DeviceInfo = deviceRaw
          ? JSON.parse(deviceRaw)
          : { last_seen: 0 };
        const signalRaw = await env.WAKEUP_KV.get(`wake-signal:${id}`);
        statuses[id] = {
          last_seen: device.last_seen,
          pending_wake: !!signalRaw,
        };
      }

      return jsonResponse({ devices: statuses });
    }

    // Route: GET /{token}/devices
    if (action === "devices" && request.method === "GET") {
      const deviceKeys = await env.WAKEUP_KV.list({ prefix: "device:" });
      const devices: Record<string, DeviceInfo> = {};

      for (const k of deviceKeys.keys) {
        const id = k.name.replace("device:", "");
        const raw = await env.WAKEUP_KV.get(k.name);
        devices[id] = raw ? JSON.parse(raw) : { last_seen: 0 };
      }

      return jsonResponse({ devices });
    }

    // Method not allowed for known routes
    if (["wake", "check", "status", "devices"].includes(action)) {
      return errorResponse("method not allowed", 405);
    }

    return errorResponse("not found", 404);
  },
};
