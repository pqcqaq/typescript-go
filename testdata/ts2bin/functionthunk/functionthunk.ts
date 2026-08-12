export interface Animal {
    readonly species: string;
}

export interface Dog extends Animal {
    readonly bark: boolean;
}

const dog: Dog = { species: "dog", bark: true };
const animal: Animal = dog;

export function source(value: Animal): Dog {
    return dog;
}

export function target(value: Dog): Animal {
	return animal;
}

export const adapted: typeof target = source;
