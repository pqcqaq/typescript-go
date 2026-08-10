function add(left: number, right: number): number {
  return left + right;
}

export function compute(left: number, right: number): number {
  let value = add(left, right);
  value = value + right;
  return value;
}
