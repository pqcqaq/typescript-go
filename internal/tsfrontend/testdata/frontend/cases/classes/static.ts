export class Factory { static make(value: number): Factory { return new Factory(value); } constructor(public value: number) {} }
