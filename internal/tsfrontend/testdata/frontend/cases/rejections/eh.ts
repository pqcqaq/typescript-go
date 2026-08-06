export function run(): number {
  try {
    throw new Error("failure");
  } catch {
    return 1;
  }
}
