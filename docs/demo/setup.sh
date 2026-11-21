#!/bin/sh
# Recreates the project of the README's demo GIF: a small todo API whose 1.4.0
# deploy, this morning, broke PATCH /todos/:id, with the morning's production log,
# and Jalon's hooks local to the project. Run it again before each take.
#
#   docs/demo/setup.sh [dir]           default ~/todo-api, the path the GIF shows
#   docs/demo/setup.sh --clean [dir]   removes the project and what its sessions left
set -eu
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)
clean_only=false
if [ "${1:-}" = --clean ]; then clean_only=true; shift; fi
dir=${1:-$HOME/todo-api}

if [ -e "$dir" ] && [ ! -e "$dir/.git/jalon-demo" ]; then
  echo "$dir exists and was not made by this script: give another path" >&2
  exit 1
fi
# Claude Code keeps a project's transcripts under a name made from its path, and
# Jalon a state file per session, named after the transcript.
transcripts="$HOME/.claude/projects/$(printf '%s' "$dir" | sed 's/[^A-Za-z0-9]/-/g')"
for t in "$transcripts"/*.jsonl; do
  [ -e "$t" ] && rm -f "$HOME/.jalon/sessions/$(basename "$t" .jsonl).json"
done
rm -rf "$dir" "$transcripts"
if $clean_only; then exit 0; fi

mkdir -p "$dir/src/routes" "$dir/test" "$dir/logs"
cd "$dir"

# Version 1.3.0: no tags yet.
cat > package.json <<'EOF'
{
  "name": "todo-api",
  "version": "1.4.0",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "tsx watch src/server.ts",
    "build": "tsc",
    "start": "node dist/server.js",
    "test": "vitest run"
  },
  "dependencies": {
    "express": "^5.1.0"
  },
  "devDependencies": {
    "@types/express": "^5.0.3",
    "@types/node": "^24.0.0",
    "@types/supertest": "^6.0.3",
    "supertest": "^7.1.4",
    "tsx": "^4.20.6",
    "typescript": "^5.9.3",
    "vitest": "^3.2.4"
  }
}
EOF
cat > tsconfig.json <<'EOF'
{
  "compilerOptions": {
    "target": "es2022",
    "module": "nodenext",
    "moduleResolution": "nodenext",
    "strict": true,
    "outDir": "dist",
    "rootDir": "src",
    "skipLibCheck": true
  },
  "include": ["src"]
}
EOF
cat > .gitignore <<'EOF'
node_modules
dist
EOF
cat > README.md <<'EOF'
# todo-api

A small REST API for todo lists: Express 5, TypeScript, storage in `data/todos.json`.

```sh
npm install
npm run dev     # http://localhost:3000
npm test
```

| Route | |
|---|---|
| `GET /todos?tag=` | list, optionally by tag |
| `POST /todos` | `{ title, tags? }` |
| `PATCH /todos/:id` | `{ title?, done?, tags? }` |
| `DELETE /todos/:id` | |
EOF
cat > src/types.ts <<'EOF'
export interface Todo {
  id: string;
  title: string;
  done: boolean;
  createdAt: string;
}
EOF
cat > src/log.ts <<'EOF'
type Fields = Record<string, unknown>;

const write = (level: string, msg: string, fields: Fields = {}) =>
  console.log(JSON.stringify({ time: new Date().toISOString(), level, msg, ...fields }));

export const log = {
  info: (msg: string, fields?: Fields) => write("info", msg, fields),
  error: (msg: string, fields?: Fields) => write("error", msg, fields),
};
EOF
cat > src/store.ts <<'EOF'
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import type { Todo } from "./types.js";

// The whole list lives in one JSON file, rewritten on every change.
export class JsonStore {
  constructor(private path: string) {}

  all(): Todo[] {
    if (!existsSync(this.path)) return [];
    return JSON.parse(readFileSync(this.path, "utf8"));
  }

  save(todos: Todo[]): void {
    writeFileSync(this.path, JSON.stringify(todos, null, 2));
  }
}
EOF
cat > src/server.ts <<'EOF'
import { createApp } from "./app.js";
import { log } from "./log.js";
import { JsonStore } from "./store.js";

const port = Number(process.env.PORT ?? 3000);
const store = new JsonStore(process.env.DATA_FILE ?? "data/todos.json");

createApp(store).listen(port, () => log.info("listening", { port, version: process.env.npm_package_version }));
EOF
cat > src/app.ts <<'EOF'
import express, { type NextFunction, type Request, type Response } from "express";
import { log } from "./log.js";
import { todosRouter } from "./routes/todos.js";
import type { JsonStore } from "./store.js";

export function createApp(store: JsonStore) {
  const app = express();
  app.use(express.json());

  app.use((req, res, next) => {
    const start = performance.now();
    res.on("finish", () =>
      log.info("request", {
        method: req.method,
        path: req.originalUrl,
        status: res.statusCode,
        ms: Math.round(performance.now() - start),
      }),
    );
    next();
  });

  app.get("/health", (_req, res) => {
    res.json({ ok: true });
  });
  app.use("/todos", todosRouter(store));

  app.use((err: Error, req: Request, res: Response, _next: NextFunction) => {
    log.error("unhandled error", {
      method: req.method,
      path: req.originalUrl,
      err: { message: err.message, stack: err.stack },
    });
    res.status(500).json({ error: "internal error" });
  });

  return app;
}
EOF
cat > src/routes/todos.ts <<'EOF'
import { randomUUID } from "node:crypto";
import { Router } from "express";
import type { JsonStore } from "../store.js";
import type { Todo } from "../types.js";

export function todosRouter(store: JsonStore): Router {
  const router = Router();

  router.get("/", (_req, res) => {
    res.json(store.all());
  });

  router.post("/", (req, res) => {
    const { title } = req.body ?? {};
    if (typeof title !== "string" || !title.trim()) {
      res.status(400).json({ error: "title is required" });
      return;
    }
    const todo: Todo = { id: randomUUID(), title: title.trim(), done: false, createdAt: new Date().toISOString() };
    store.save([...store.all(), todo]);
    res.status(201).json(todo);
  });

  router.patch("/:id", (req, res) => {
    const todos = store.all();
    const todo = todos.find((t) => t.id === req.params.id);
    if (!todo) {
      res.status(404).json({ error: "not found" });
      return;
    }
    const { title, done } = req.body ?? {};
    if (title !== undefined) todo.title = String(title).trim();
    if (done !== undefined) todo.done = Boolean(done);
    store.save(todos);
    res.json(todo);
  });

  router.delete("/:id", (req, res) => {
    const todos = store.all();
    const next = todos.filter((t) => t.id !== req.params.id);
    if (next.length === todos.length) {
      res.status(404).json({ error: "not found" });
      return;
    }
    store.save(next);
    res.status(204).end();
  });

  return router;
}
EOF
cat > test/todos.test.ts <<'EOF'
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import request from "supertest";
import { beforeEach, describe, expect, it } from "vitest";
import { createApp } from "../src/app.js";
import { JsonStore } from "../src/store.js";

let app: ReturnType<typeof createApp>;

beforeEach(() => {
  app = createApp(new JsonStore(join(mkdtempSync(join(tmpdir(), "todos-")), "todos.json")));
});

describe("todos", () => {
  it("creates a todo", async () => {
    const res = await request(app).post("/todos").send({ title: "  Buy milk " });
    expect(res.status).toBe(201);
    expect(res.body).toMatchObject({ title: "Buy milk", done: false });
  });

  it("rejects a todo without a title", async () => {
    const res = await request(app).post("/todos").send({});
    expect(res.status).toBe(400);
  });

  it("marks a todo done", async () => {
    const { body } = await request(app).post("/todos").send({ title: "Write tests" });
    const res = await request(app).patch(`/todos/${body.id}`).send({ done: true });
    expect(res.status).toBe(200);
    expect(res.body.done).toBe(true);
  });

  it("deletes a todo", async () => {
    const { body } = await request(app).post("/todos").send({ title: "Temp" });
    expect((await request(app).delete(`/todos/${body.id}`)).status).toBe(204);
    expect((await request(app).get("/todos")).body).toEqual([]);
  });
});
EOF

npm install --silent --no-audit --no-fund
git init -q -b main
git config user.name "Sam Rivera"
git config user.email "sam@example.com"
commit() { GIT_AUTHOR_DATE="$1" GIT_COMMITTER_DATE="$1" git commit -q -m "$2"; }
git add -- . ':!test' && commit "2026-10-02T10:14:00+02:00" "feat: todo API with JSON file storage"
git add test && commit "2026-10-03T16:40:00+02:00" "test: cover create, update and delete"

# Version 1.4.0, deployed this morning: tags, and a PATCH that spreads todo.tags,
# which the todos saved before it do not have.
git apply <<'PATCH_EOF'
diff --git a/src/routes/todos.ts b/src/routes/todos.ts
index e6909df..8b639fa 100644
--- a/src/routes/todos.ts
+++ b/src/routes/todos.ts
@@ -1,22 +1,31 @@
 import { randomUUID } from "node:crypto";
 import { Router } from "express";
 import type { JsonStore } from "../store.js";
+import { normalizeTags } from "../tags.js";
 import type { Todo } from "../types.js";
 
 export function todosRouter(store: JsonStore): Router {
   const router = Router();
 
-  router.get("/", (_req, res) => {
-    res.json(store.all());
+  router.get("/", (req, res) => {
+    const tag = req.query.tag as string | undefined;
+    const todos = store.all();
+    res.json(tag ? todos.filter((t) => t.tags?.includes(tag)) : todos);
   });
 
   router.post("/", (req, res) => {
-    const { title } = req.body ?? {};
+    const { title, tags = [] } = req.body ?? {};
     if (typeof title !== "string" || !title.trim()) {
       res.status(400).json({ error: "title is required" });
       return;
     }
-    const todo: Todo = { id: randomUUID(), title: title.trim(), done: false, createdAt: new Date().toISOString() };
+    const todo: Todo = {
+      id: randomUUID(),
+      title: title.trim(),
+      done: false,
+      createdAt: new Date().toISOString(),
+      tags: normalizeTags(tags),
+    };
     store.save([...store.all(), todo]);
     res.status(201).json(todo);
   });
@@ -28,9 +37,10 @@ export function todosRouter(store: JsonStore): Router {
       res.status(404).json({ error: "not found" });
       return;
     }
-    const { title, done } = req.body ?? {};
+    const { title, done, tags = [] } = req.body ?? {};
     if (title !== undefined) todo.title = String(title).trim();
     if (done !== undefined) todo.done = Boolean(done);
+    todo.tags = normalizeTags([...todo.tags, ...tags]);
     store.save(todos);
     res.json(todo);
   });
diff --git a/src/tags.ts b/src/tags.ts
new file mode 100644
index 0000000..46d0c77
--- /dev/null
+++ b/src/tags.ts
@@ -0,0 +1,3 @@
+export function normalizeTags(tags: string[]): string[] {
+  return [...new Set(tags.map((t) => t.trim().toLowerCase()).filter(Boolean))];
+}
diff --git a/src/types.ts b/src/types.ts
index 723c434..9664a7e 100644
--- a/src/types.ts
+++ b/src/types.ts
@@ -3,4 +3,5 @@ export interface Todo {
   title: string;
   done: boolean;
   createdAt: string;
+  tags: string[];
 }
diff --git a/test/todos.test.ts b/test/todos.test.ts
index 6d501e8..66c0a84 100644
--- a/test/todos.test.ts
+++ b/test/todos.test.ts
@@ -31,6 +31,20 @@ describe("todos", () => {
     expect(res.body.done).toBe(true);
   });
 
+  it("normalizes tags and filters by tag", async () => {
+    await request(app).post("/todos").send({ title: "Pay rent", tags: [" Home ", "home", "Money"] });
+    await request(app).post("/todos").send({ title: "Fix bike" });
+    const res = await request(app).get("/todos?tag=home");
+    expect(res.body).toHaveLength(1);
+    expect(res.body[0].tags).toEqual(["home", "money"]);
+  });
+
+  it("adds tags to an existing todo", async () => {
+    const { body } = await request(app).post("/todos").send({ title: "Call mom", tags: ["family"] });
+    const res = await request(app).patch(`/todos/${body.id}`).send({ tags: ["Weekend"] });
+    expect(res.body.tags).toEqual(["family", "weekend"]);
+  });
+
   it("deletes a todo", async () => {
     const { body } = await request(app).post("/todos").send({ title: "Temp" });
     expect((await request(app).delete(`/todos/${body.id}`)).status).toBe(204);
PATCH_EOF
day=$(date +%F)
git add -A && commit "${day}T08:05:00+01:00" "feat(todos): tags on todos, filter with ?tag="
node "$here/log.mjs" "$day" logs/server.log

# Jalon built from this repository, in hooks local to the project, with the
# thresholds of jalon.yaml, and its /jalon-* commands.
go -C "$repo" build -o "$here/.bin/jalon" ./cmd/jalon
mkdir -p .claude
cat > .claude/settings.json <<EOF
{
  "hooks": {
    "UserPromptSubmit": [{ "hooks": [{ "type": "command", "command": "jalon hook prompt --client claude-code --config $here/jalon.yaml", "timeout": 2 }] }],
    "Stop": [{ "hooks": [{ "type": "command", "command": "jalon hook stop --client claude-code --config $here/jalon.yaml", "timeout": 2 }] }]
  },
  "statusLine": { "type": "command", "command": "jalon statusline --client claude-code --config $here/jalon.yaml" },
  "attribution": { "commit": "", "pr": "" },
  "includeCoAuthoredBy": false
}
EOF
for s in "$repo"/internal/client/claudecode/skills/jalon-*; do
  mkdir -p ".claude/skills/${s##*/}"
  sed 's/{jalon}/jalon/g' "$s/SKILL.md" > ".claude/skills/${s##*/}/SKILL.md"
done
printf '.claude/\n.jalon/\n' >> .git/info/exclude
touch .git/jalon-demo
echo "Ready: $dir"
