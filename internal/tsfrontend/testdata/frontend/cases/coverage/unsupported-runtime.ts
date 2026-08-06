const deletable: { value?: number } = { value: 1 };
delete deletable.value;

for (const key in deletable) { void key; }

async function load(): Promise<number> {
  await Promise.resolve(1);
  return 1;
}

function* values(): Generator<number> {
  yield 1;
}

const scope = { value: 1 };
// @ts-ignore: the subset gate must reject the source-level with statement.
with (scope) {}

void load;
void values;
