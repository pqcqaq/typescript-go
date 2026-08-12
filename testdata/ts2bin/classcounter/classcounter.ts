class Counter {
  value: number = 0;

  constructor(start: number) {
    this.value = start;
  }

  increment(): number {
    this.value += 1;
    return this.value;
  }
}

export function classCounter(start: number): number {
  const counter = new Counter(start);
  return counter.increment() + counter.increment();
}
