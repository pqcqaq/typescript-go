// @ts-check

/** @import { Foo } from "./coverage-dep" */
/** @typedef {{ value: number }} JSValue */

/** @type {JSValue} */
const value = { value: 1 };

/** @type {Foo} */
const imported = value;

export { imported, value };
