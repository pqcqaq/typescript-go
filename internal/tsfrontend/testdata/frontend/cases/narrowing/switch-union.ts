type State = "idle" | "running" | "done";
export function rank(state: State): number { switch (state) { case "idle": return 0; case "running": return 1; case "done": return 2; } }
