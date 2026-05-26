// Fixture: violates ADR-0047 ESLint config's react/no-danger ban on
// dangerouslySetInnerHTML. The cockpit will render operator-controllable
// content (logs, terminal output); this is the XSS escape hatch we
// explicitly do not permit.

export function BadDanger({ html }: { html: string }) {
  // Should trigger react/no-danger.
  return <div dangerouslySetInnerHTML={{ __html: html }} />;
}
