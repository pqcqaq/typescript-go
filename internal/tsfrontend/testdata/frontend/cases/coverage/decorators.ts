function mark(target: Function): void { void target; }

@mark
export class Decorated {
  static value = 0;
  static { this.value = 1; }
}
