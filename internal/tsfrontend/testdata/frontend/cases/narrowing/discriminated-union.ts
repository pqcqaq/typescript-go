type Shape = { kind: "circle"; radius: number } | { kind: "square"; size: number };
export function area(shape: Shape): number { return shape.kind === "circle" ? shape.radius * shape.radius : shape.size * shape.size; }
