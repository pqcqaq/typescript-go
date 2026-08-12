class Vault {
  private secret: number = 1;
  protected value: number = 2;

  readSecret(other: Vault): number {
    return other.secret;
  }
}

class DerivedVault extends Vault {
  readValue(other: DerivedVault): number {
    return other.value;
  }
}

export function classAccess(): number {
  const vault = new DerivedVault();
  return vault.readSecret(vault) + vault.readValue(vault);
}
