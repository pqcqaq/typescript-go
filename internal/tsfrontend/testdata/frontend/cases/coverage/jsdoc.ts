class Target {}

/**
 * A documented value with {@link Target}, {@linkcode Target}, and
 * {@linkplain Target readable text} links.
 * @template T
 * @param {T} value
 * @param {*} anyValue
 * @param {?number} nullableValue
 * @param {!string} nonNullableValue
 * @param {number=} optionalValue
 * @param {...string} variadicValue
 * @returns {T}
 * @this {Target}
 * @throws {Error}
 * @deprecated use replacement
 * @public
 * @private
 * @protected
 * @readonly
 * @override
 * @see Target
 * @satisfies {Target}
 * @import { Foo } from "./coverage-dep"
 * @custom unsupported tag
 */
function documented<T>(value: T, anyValue: number, nullableValue: number | null, nonNullableValue: string, optionalValue = 0, variadicValue = ""): T {
  void anyValue;
  void nullableValue;
  void nonNullableValue;
  void optionalValue;
  void variadicValue;
  return value;
}

interface Options { name: string; count: number | null; }

/**
 * @typedef {Object} OptionsDoc
 * @property {string} name
 * @property {?number} count
 */
const options: Options = { name: "coverage", count: 1 };

/** @callback Callback
 * @param {number} value
 * @returns {string}
 */
function callback(value: number): string { return String(value); }

/** @overload */
function overloaded(value: number): number { return value; }

/** @augments Target @implements {Target} */
class Child extends Target {}

/** @type {Options} */
const typedOptions: Options = options;

void documented;
void callback;
void overloaded;
void Child;
void typedOptions;
