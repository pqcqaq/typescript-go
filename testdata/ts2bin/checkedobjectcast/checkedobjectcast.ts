interface HostValue {
  readonly value: number;
}

type HostShape = "matching" | "missing";

// The ambient declaration is the only admissible first-slice dynamic boundary.
// TypeScript assertions remain deliberately outside the checked-cast contract.
declare function hostObject(shape: HostShape): unknown;

export declare function checkedObjectCastTarget(value: HostValue): HostValue;
