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

  async scheduleMonthlyUpdate() {
    const now = new Date();
    const nextMonth = new Date(now.getFullYear(), now.getMonth() + 1, 1, 3, 0, 0);
    const delayMs = nextMonth.getTime() - now.getTime();
    await this.schedule(Math.floor(delayMs / 1000), "monthlyMapUpdate");
  }

  async monthlyMapUpdate() {
    console.log("Monthly map update: restarting container to reprocess data");
    await this.stop();
    await this.scheduleMonthlyUpdate();
  }
}

export default {
  async fetch(request, env) {
    const container = getContainer(env.OSRM_CONTAINER, "osrm");

    try {
      const state = await container.getState();
      if (state.status === "running" || state.status === "healthy") {
        return container.fetch(request);
      }
    } catch {}

    return container.fetch(request);
  },

  async scheduled(event, env) {
    const container = getContainer(env.OSRM_CONTAINER, "osrm");
    await container.start();
    await container.scheduleMonthlyUpdate();
    return new Response("Monthly update triggered");
  },
};
