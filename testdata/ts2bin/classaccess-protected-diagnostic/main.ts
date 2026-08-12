class Vault {
  protected value = 1;
}

class DerivedVault extends Vault {
  read(other: Vault): number {
    return other.value;
  }
}
