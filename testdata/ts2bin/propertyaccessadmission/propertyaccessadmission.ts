interface Pair {
  readonly left: number;
  readonly right: number;
}

type PairKey = "left" | "right";

declare function hostRecord(): Readonly<Record<string, number>>;

export function direct(pair: Pair): number {
  return pair.left;
}

export function literal(pair: Pair): number {
  return pair["right"];
}

export function finite(pair: Pair, key: PairKey): number {
  return pair[key];
}

export function dynamic(key: string): number {
  return hostRecord()[key];
}
