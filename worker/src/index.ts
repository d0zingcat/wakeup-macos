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

function jsonResponse(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function errorResponse(message: string, status: number): Response {
  return jsonResponse({ error: message }, status);
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
          await env.WAKEUP_KV.put(`wake-signal:${id}`, JSON.stringify(signal), {
            expirationTtl: 900, // 15 minutes
          });
        }
        return jsonResponse({ ok: true, devices: ids, duration });
      }

      if (!deviceId) {
        return errorResponse("device_id required", 400);
      }

      const signal: WakeSignal = {
        wake: true,
        duration,
        created_at: Date.now(),
      };
      await env.WAKEUP_KV.put(
        `wake-signal:${deviceId}`,
        JSON.stringify(signal),
        { expirationTtl: 900 },
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
      const key = `wake-signal:${deviceId}`;
      const raw = await env.WAKEUP_KV.get(key);
      if (!raw) {
        return jsonResponse({ wake: false });
      }

      await env.WAKEUP_KV.delete(key);
      const signal: WakeSignal = JSON.parse(raw);
      return jsonResponse(signal);
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
