// agent-quorum plugin for OpenCode.
//
// OpenCode plugins must be JavaScript or TypeScript, so this file only
// registers hooks, maps OpenCode's objects onto the bridge schema owned by
// internal/adapter/opencode, runs `agent-quorum hook --agent opencode` and
// applies its answer. Every coordination decision is made in Go.
//
// It FAILS OPEN: any error, timeout or missing binary leaves OpenCode's
// behaviour unchanged. It never sets a permission to "allow".
//
// OpenCode runs this file directly; there is no build step and no
// dependency. The interfaces below describe only the fields used here.
import { spawn, type ChildProcess } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

interface SessionInfo {
  parentID?: string;
  location?: { directory?: string };
}

interface PromptEvent {
  sessionID?: string;
  prompt?: string | { text?: string; parts?: { text?: string }[] };
}

interface ContextEvent {
  sessionID?: string;
  system?: { type: string; text: string }[];
}

interface PermissionEvent {
  sessionID?: string;
  action?: string;
  resources?: string[];
  effect?: string;
  message?: string;
}

interface BusEvent {
  type?: string;
  data?: { sessionID?: string; parentID?: string; location?: { directory?: string } };
}

type Cleanup = (() => unknown) | undefined;

interface PluginContext {
  location?: { directory?: string };
  session: {
    get(input: { sessionID: string }): Promise<SessionInfo | null | undefined>;
    hook(name: "prompt", fn: (e: PromptEvent) => unknown): Promise<Cleanup>;
    hook(name: "context", fn: (e: ContextEvent) => unknown): Promise<Cleanup>;
  };
  permission: {
    hook(name: "evaluate", fn: (e: PermissionEvent) => unknown): Promise<Cleanup>;
  };
  event: {
    subscribe(input: { signal: AbortSignal }): AsyncIterable<BusEvent>;
  };
}

// Bridge is the schema of internal/adapter/opencode: what the binary reads
// on stdin and what it may print back.
interface BridgeEvent {
  event: "prompt" | "permission" | "end";
  session_id: string;
  directory: string;
  action?: string;
  resources?: string[];
  prompt?: string;
  pid: number;
}

interface BridgeAnswer {
  context?: string;
  deny?: string;
}

interface Session {
  root: string;
  directory: string;
}

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const quorumHome = process.env.AGENT_QUORUM_HOME || join(homedir(), ".agent-quorum");
const timeoutMs = 2000; // above the binary's own 1.5 s budget
const maxDepth = 8; // subagent nesting followed when looking for the root session
const editActions = new Set(["edit", "write", "patch"]);
const shellActions = new Set(["shell", "bash"]);

// The process whose life bounds a session. `opencode run --standalone` hosts
// the plugin in a server that exits with the run. By default OpenCode hosts it
// in one shared background service that outlives every session, so report 0
// (unknown) and let the daemon fall back to its idle timeout.
const sharedService = process.ppid === 1 || process.argv.includes("--service");
const pid = sharedService ? 0 : process.pid;

function pluginVersion(): string {
  try {
    const manifest = readFileSync(join(root, ".claude-plugin", "plugin.json"), "utf8");
    return manifest.match(/"version"\s*:\s*"([^"]+)"/)?.[1] ?? "";
  } catch {
    return "";
  }
}

// Same resolution order as plugin/scripts/agent-quorum.sh.
function findBinary(): string {
  const dev = process.env.AGENT_QUORUM_BIN;
  if (dev && existsSync(dev)) return dev;
  const version = pluginVersion();
  const installed = join(quorumHome, "bin", `agent-quorum-${version}`);
  if (version && existsSync(installed)) return installed;
  return "agent-quorum"; // PATH; a spawn error is no opinion
}

// call sends one bridge event and resolves to the parsed answer, or null for
// no opinion. The binary is spawned directly, without a shell, and gets the
// session's liveness pid from the event, not from its parent process.
function call(bin: string, event: BridgeEvent): Promise<BridgeAnswer | null> {
  return new Promise((resolve) => {
    let out = "";
    let child: ChildProcess | undefined;
    const timer = setTimeout(() => {
      try {
        child?.kill();
      } catch {}
      done(null);
    }, timeoutMs);
    function done(value: BridgeAnswer | null) {
      clearTimeout(timer);
      resolve(value);
    }
    try {
      child = spawn(bin, ["hook", "--agent", "opencode"], { stdio: ["pipe", "pipe", "ignore"] });
      child.on("error", () => done(null));
      child.stdout?.on("data", (chunk) => (out += chunk));
      child.on("close", () => {
        try {
          done(out.trim() ? (JSON.parse(out) as BridgeAnswer) : null);
        } catch {
          done(null);
        }
      });
      child.stdin?.on("error", () => {});
      child.stdin?.end(JSON.stringify(event));
    } catch {
      done(null);
    }
  });
}

function installInBackground(): void {
  if (process.env.AGENT_QUORUM_BIN) return;
  try {
    spawn(join(root, "scripts", "install.sh"), [], { detached: true, stdio: "ignore" }).unref();
  } catch {}
}

function promptText(prompt: PromptEvent["prompt"]): string {
  if (typeof prompt === "string") return prompt;
  if (typeof prompt?.text === "string") return prompt.text;
  return (prompt?.parts ?? [])
    .map((p) => (typeof p?.text === "string" ? p.text : ""))
    .filter(Boolean)
    .join("\n");
}

export default {
  id: "agent-quorum",

  async setup(ctx: PluginContext) {
    const bin = findBinary();
    if (bin === "agent-quorum") installInBackground();

    const infos = new Map<string, SessionInfo>(); // sessionID -> ctx.session.get result
    const parents = new Map<string, string>(); // child sessionID -> parentID, from events
    const peerContext = new Map<string, string>(); // root -> first peer context

    async function info(sessionID: string): Promise<SessionInfo | undefined> {
      const known = infos.get(sessionID);
      if (known) return known;
      const got = await ctx.session.get({ sessionID }).catch(() => null);
      if (got) infos.set(sessionID, got);
      return got ?? undefined;
    }

    // session resolves the root on every call rather than caching it: a
    // subagent's session.created event can arrive after its first hook, and
    // caching the child as its own root would deny it its parent's files.
    async function session(sessionID: string): Promise<Session> {
      const directory = (await info(sessionID))?.location?.directory || ctx.location?.directory || "";
      // Subagents run in child sessions; they share their root's claims.
      let root = sessionID;
      for (let i = 0; i < maxDepth; i++) {
        const up = parents.get(root) ?? (await info(root))?.parentID;
        if (!up) break;
        root = up;
      }
      return { root, directory };
    }

    async function bridge(sessionID: string, fields: Omit<BridgeEvent, "session_id" | "directory">) {
      const s = await session(sessionID);
      return call(bin, { ...fields, session_id: s.root, directory: s.directory, pid });
    }

    const cleanups: Cleanup[] = [];

    cleanups.push(
      await ctx.session.hook("prompt", async (e) => {
        try {
          const prompt = promptText(e.prompt);
          if (!e.sessionID || !prompt) return;
          // A subagent's prompt is its task, not what the root session is
          // working on, so it must not replace the root's note.
          const { root } = await session(e.sessionID);
          if (root !== e.sessionID) return;
          const answer = await bridge(e.sessionID, { event: "prompt", prompt });
          // Keep the first context only, as Claude Code and Codex do: the
          // system prompt heads every request, so changing it whenever a peer
          // moves would invalidate the cached history every turn. Denies
          // still name the current holder.
          if (answer?.context && !peerContext.has(root)) peerContext.set(root, answer.context);
        } catch {}
      }),
    );

    // Push the same text on every model step so the system prompt stays
    // byte-identical for the whole session.
    cleanups.push(
      await ctx.session.hook("context", async (e) => {
        try {
          if (!e.sessionID || !Array.isArray(e.system)) return;
          const text = peerContext.get((await session(e.sessionID)).root);
          if (text) e.system.push({ type: "text", text });
        } catch {}
      }),
    );

    cleanups.push(
      await ctx.permission.hook("evaluate", async (e) => {
        try {
          if (!e.sessionID || !e.action || e.effect === "deny") return;
          const resources = Array.isArray(e.resources) ? e.resources : [];
          // Like the Claude hook's `if: Bash(git *)`: only spares a process
          // spawn for commands that cannot be risky git. The guard decides.
          const guarded =
            editActions.has(e.action) || (shellActions.has(e.action) && /\bgit\b/.test(resources.join("\n")));
          if (!guarded) return;
          const answer = await bridge(e.sessionID, { event: "permission", action: e.action, resources });
          if (answer?.deny) {
            e.effect = "deny";
            e.message = answer.deny;
          }
        } catch {}
      }),
    );

    // Only a root session's deletion ends it: a finished subagent must not
    // drop its parent's claims. Server exit and crashes are covered by the
    // daemon's pid liveness check, so unload sends nothing.
    const abort = new AbortController();
    void (async () => {
      try {
        for await (const ev of ctx.event.subscribe({ signal: abort.signal })) {
          try {
            const id = ev?.data?.sessionID;
            if (!id) continue;
            if (ev.type === "session.created" && ev.data?.parentID) parents.set(id, ev.data.parentID);
            if (ev.type !== "session.deleted") continue;
            const known = infos.get(id);
            const isRoot = known && !known.parentID && !parents.has(id);
            infos.delete(id);
            parents.delete(id);
            if (isRoot) {
              peerContext.delete(id);
              const directory = known.location?.directory || ctx.location?.directory || "";
              await call(bin, { event: "end", session_id: id, directory, pid });
            }
          } catch {}
        }
      } catch {}
    })();

    return async () => {
      abort.abort();
      for (const cleanup of cleanups) {
        try {
          if (typeof cleanup === "function") await cleanup();
        } catch {}
      }
    };
  },
};
