export interface Animal {
    readonly species: string;
}

export interface Dog extends Animal {
    readonly bark: boolean;
}
