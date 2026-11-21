// Writes a morning of production log around the 1.4.0 deploy, for setup.sh.
// Usage: node log.mjs <day as YYYY-MM-DD> <output file>. The same day gives the same log.
import { writeFileSync } from "node:fs";
let seed = 7;
const rnd = () => ((seed = (seed * 16807) % 2147483647) / 2147483647);
const pick = (a) => a[Math.floor(rnd() * a.length)];
const int = (a, b) => a + Math.floor(rnd() * (b - a + 1));
const uuid = () => "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx".replace(/[xy]/g, (c) => ((c === "x" ? int(0, 15) : int(8, 11)).toString(16)));
const STACK = (id) => `TypeError: todo.tags is not iterable\n    at file:///app/dist/routes/todos.js:39:44\n    at Layer.handleRequest (/app/node_modules/router/lib/layer.js:169:33)\n    at next (/app/node_modules/router/lib/route.js:163:13)\n    at Route.dispatch (/app/node_modules/router/lib/route.js:123:3)\n    at handle (/app/node_modules/router/index.js:449:11)\n    at Layer.handleRequest (/app/node_modules/router/lib/layer.js:169:33)\n    at /app/node_modules/router/index.js:309:15\n    at param (/app/node_modules/router/index.js:637:14)\n    at param (/app/node_modules/router/index.js:647:14)\n    at processParams (/app/node_modules/router/index.js:700:3)`;

const [day, file] = process.argv.slice(2);
const start = Date.parse(`${day}T06:40:00Z`), deploy = Date.parse(`${day}T07:11:40Z`), end = Date.parse(`${day}T09:25:00Z`);
const old = Array.from({ length: 28 }, uuid); // created before tags existed
const fresh = [];
const lines = [];
const out = (t, o) => lines.push([t, JSON.stringify({ time: new Date(t).toISOString(), ...o })]);
const req = (t, method, path, status, ms) => out(t, { level: "info", msg: "request", method, path, status, ms });

for (let t = start; t < end; t += 120_000) req(t + int(0, 900), "GET", "/health", 200, int(0, 1));
out(deploy, { level: "info", msg: "shutting down", signal: "SIGTERM" });
out(deploy + 11_800, { level: "info", msg: "listening", port: 3000, version: "1.4.0" });

for (let t = start + 7_000; t < end; t += int(20_000, 75_000)) {
  const after = t > deploy + 12_000, r = rnd();
  if (r < 0.38) req(t, "GET", after && rnd() < 0.3 ? `/todos?tag=${pick(["work", "home", "groceries"])}` : "/todos", 200, int(2, 9));
  else if (r < 0.55) {
    const id = uuid();
    (after ? fresh : old).push(id);
    req(t, "POST", "/todos", rnd() < 0.05 ? 400 : 201, int(3, 12));
  } else if (r < 0.92) {
    const isOld = !after || fresh.length === 0 || rnd() < 0.6;
    const id = isOld ? pick(old) : pick(fresh);
    if (after && isOld) {
      out(t, { level: "error", msg: "unhandled error", method: "PATCH", path: `/todos/${id}`, err: { message: "todo.tags is not iterable", stack: STACK(id) } });
      req(t + 2, "PATCH", `/todos/${id}`, 500, int(2, 5));
    } else req(t, "PATCH", `/todos/${id}`, 200, int(3, 11));
  } else {
    const list = rnd() < 0.5 && fresh.length ? fresh : old;
    const id = list.splice(Math.floor(rnd() * list.length), 1)[0]; // deleted for good
    req(t, "DELETE", `/todos/${id}`, 204, int(2, 8));
  }
}
lines.sort((a, b) => a[0] - b[0]);
writeFileSync(file, lines.map((l) => l[1]).join("\n") + "\n");
