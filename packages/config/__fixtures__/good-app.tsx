// Fixture: clean code under the shared ESLint config. Should produce
// zero errors.

interface Props {
  name: string;
}

export function GoodComponent({ name }: Props) {
  return <span>{name}</span>;
}
