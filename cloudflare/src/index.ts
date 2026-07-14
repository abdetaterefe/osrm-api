import { Container, getContainer } from "@cloudflare/containers";

interface Env {
  OSRM_CONTAINER: typeof OsrmContainer;
}

export class OsrmContainer extends Container<Env> {
  defaultPort = 3000;
  sleepAfter = "15m";
  requiredPorts = [3000, 5001, 5002, 5003, 5004];

  override onStart(): void {
    console.log("OSRM container started");
  }

  override onStop(): void {
    console.log("OSRM container stopped");
  }

  override onError(error: Error): void {
    console.error("OSRM container error:", error);
  }

  async scheduleMonthlyUpdate(): Promise<void> {
    const now = new Date();
    const nextMonth = new Date(now.getFullYear(), now.getMonth() + 1, 1, 3, 0, 0);
    const delayMs = nextMonth.getTime() - now.getTime();
    await this.schedule(Math.floor(delayMs / 1000), "monthlyMapUpdate");
  }

  async monthlyMapUpdate(): Promise<void> {
    console.log("Monthly map update: restarting container to reprocess data");
    await this.stop();
    await this.scheduleMonthlyUpdate();
  }
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const container = getContainer(env.OSRM_CONTAINER, "osrm");

    try {
      const state = await container.getState();
      if (state.status === "running" || state.status === "healthy") {
        return container.fetch(request);
      }
    } catch {}

    return container.fetch(request);
  },

  async scheduled(_event: ScheduledEvent, env: Env): Promise<Response> {
    const container = getContainer(env.OSRM_CONTAINER, "osrm");
    await container.start();
    await container.scheduleMonthlyUpdate();
    return new Response("Monthly update triggered");
  },
};
