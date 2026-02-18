import { Hono } from "hono";
import { createKagami, TunnelDO } from "kagami";

type Env = {
  TUNNEL: DurableObjectNamespace<TunnelDO>;
  KAGAMI_DB: D1Database;
  KAGAMI_PROJECT_SECRET: string;
  KAGAMI_BASE_DOMAIN: string;
};

const app = new Hono<{ Bindings: Env }>();
const kagami = createKagami();

// Subdomain requests (*.BASE_DOMAIN) are proxied to the matching DO
app.use("*", kagami.proxy);

// Management routes (only reached on base domain -- not subdomains)
app.route("/_kagami", kagami.routes);

// User's own routes
app.get("/", (c) => c.text("My app"));

export { TunnelDO };
export default app;
