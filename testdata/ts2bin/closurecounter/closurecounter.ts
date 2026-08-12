function makeCounter(start: number): () => number {
  let count = start;
  return () => {
    count += 1;
    return count;
  };
}

export function closureCounter(start: number): number {
  const increment = makeCounter(start);
  return increment() + increment();
}
