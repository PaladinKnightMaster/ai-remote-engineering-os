// apps/web — ESLint flat config. Re-exports the shared rules from
// @aireos/config so SEC-7 enforcement is identical across every
// workspace consumer. To override locally, append a config object
// after `shared` — but cite the doctrine you're loosening.
import shared from "@aireos/config/eslint.config.js";
export default shared;
