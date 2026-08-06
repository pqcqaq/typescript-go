export interface Callable {
  (value: number): string;
  new (value: number): object;
  [key: string]: unknown;
  method?(value?: number, ...rest: string[]): this;
}

type Constructor = new (value: number) => object;
type Tuple = [head: string, tail?: number, ...rest: boolean[]];
type UnnamedTuple = [string?, ...number[]];
type Mixed = { a: number } & ({ b?: number });
type Inferred<T> = T extends Promise<infer U> ? U : never;
type Template<T extends string> = `hello ${T}`;
type Qualified = Intl.DateTimeFormatOptions;
type Imported = import("./coverage-dep").Foo;

const source = { value: 1, nested: { value: 2 } };
const indexed = source["value"];
const { value: bound, nested: { value: nestedValue }, ...objectRest } = source;
const tupleSource: [number, number, number] = [1, 2, 3];
const [first, , ...arrayRest] = tupleSource;
const computed = "value";

class Example {
  [computed] = 1;
  ;
  static field = 0;
  method(value: number) { return value; }
}

const functionValue = function (value: number) { return value; };
const parenthesized = (1 + 2);
const typedOne: number = 1;
const asserted = (typedOne as number);
const angleAsserted = <number>typedOne;
const satisfied = typedOne satisfies number;
const tagged = String.raw`prefix ${bound} middle ${nestedValue} suffix`;
const templated = `prefix ${bound} middle ${nestedValue} suffix`;
const array = [1, ...arrayRest, , 3];
const spreadObject = { ...source, bound };
const classValue = class { value = 1; };
function meta() { return new.target; }
const voided = void 0;
let empty: number;
;
do { empty = 1; } while (false);
while (false) { break; }
for (let i = 0; i < 1; i++) { continue; }
for (const item of tupleSource) { empty = item; }
label: { break label; }
debugger;
switch (empty) { case 1: break; default: empty = 0; }

import * as namespaceImport from "./coverage-dep";
export * as namespaceExport from "./coverage-dep";
export { namespaceImport };
export default 42;

void objectRest;
void indexed;
void first;
void functionValue;
void Example;
void classValue;
void tagged;
void templated;
void array;
void satisfied;
void asserted;
void angleAsserted;
void voided;
