import { Container, getContainer } from "@cloudflare/containers";

export class OsrmContainer extends Container {
  defaultPort = 3000;
  sleepAfter = "15m";

  requiredPorts = [3000, 5001, 5002, 5003, 5004];

  override onStart() {
    console.log("OSRM container started");
  }

  override onStop() {
    console.log("OSRM container stopped");
  }

  override onError(error) {
    console.error("OSRM container error:", error);
  }
}

export default {
  async fetch(request, env) {
    const url = new URL(request.url);

    if (url.pathname === "/health") {
      try {
        const container = getContainer(env.OSRM_CONTAINER, "osrm");
        const res = await container.fetch(
          new Request("http://localhost/health")
        );
        return res;
      } catch {
        return new Response(
          JSON.stringify({ status: "starting" }),
          { headers: { "Content-Type": "application/json" } }
        );
      }
    }

    const container = getContainer(env.OSRM_CONTAINER, "osrm");
    return container.fetch(request);
  },
};
