import type { Animal, Dog } from "./models";

export interface ReadonlyBox<out T> {
    readonly value: T;
}

export interface Consumer<in T> {
    consume(value: T): void;
}

export interface Cell<T> {
    value: T;
}

export declare function upcastBox(value: ReadonlyBox<Dog>): ReadonlyBox<Animal>;

export interface Tree<T> {
    readonly value: T;
    readonly child: Tree<T>;
}

export interface RecursiveProducer<T> {
    readonly value: T;
    readonly consumer: RecursiveConsumer<T>;
}

export interface RecursiveConsumer<T> {
    consume(value: RecursiveProducer<T>): void;
}

export type Residual<T> = {
    [K in keyof T]: T[K];
};
