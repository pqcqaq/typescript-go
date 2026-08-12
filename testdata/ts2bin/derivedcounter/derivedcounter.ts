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

class StepCounter extends Counter {
  step: number = 1;

  constructor(start: number, step: number) {
    super(start);
    this.step = step;
  }

  increment(): number {
    this.value += this.step;
    return this.value;
  }
}

export function derivedCounter(start: number, step: number): number {
  const counter = new StepCounter(start, step);
  return counter.increment() + counter.increment();
}
