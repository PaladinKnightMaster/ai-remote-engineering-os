// Fixture: directory-name bypass attempt. The DIRECTORY is named
// `oauth` (matches the forbidden-features pattern) but the FILE inside
// is benignly named `handler.go`. Without the directory-name check
// added in the T-011 review pass, this would slip past.
package oauth

type Handler struct{}
