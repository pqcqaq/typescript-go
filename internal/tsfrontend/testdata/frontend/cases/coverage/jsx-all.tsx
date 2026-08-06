declare namespace JSX {
  interface IntrinsicElements {
    div: { title?: string; dataValue?: number; children?: unknown };
    "svg:path": {};
  }
}

const props = { dataValue: 1 };
const element = (
  <>
    <div title="coverage" {...props}>
      visible text {props.dataValue}
    </div>
    <svg:path />
  </>
);

void element;
