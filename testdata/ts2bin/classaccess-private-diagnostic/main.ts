class Vault {
  private secret = 1;
}

class DerivedVault extends Vault {
  read(): number {
    return this.secret;
  }
}
