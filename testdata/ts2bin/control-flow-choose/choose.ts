export function choose(flag: boolean, left: number, right: number): number {
    if (flag) {
        return left;
    }
    return right;
}
