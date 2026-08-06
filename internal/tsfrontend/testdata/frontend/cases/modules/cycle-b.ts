import { a } from "./cycle-a";
export const b: number = 1;
export const seen: number = a;
