// Command authctl is the operator provisioning helper for the in-process auth
// model (ADR-0046). It generates the cluster signing key and argon2id password
// hashes for the bootstrap operator account — the values install.sh / the
// operator set as env vars (AIREOS_PASETO_KEY,
// AIREOS_BOOTSTRAP_OPERATOR_PASSWORD_HASH). A full install.sh is a later devops
// ticket; this is the primitive it would call.
package main

import (
	"fmt"
	"os"

	"aireos.local/packages/go/auth"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "authctl:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "genkey":
		_, hexKey := auth.GenerateKey()
		fmt.Println(hexKey)
		return nil
	case "hash":
		if len(args) < 2 {
			return fmt.Errorf("usage: authctl hash <password>")
		}
		h, err := auth.HashPassword(args[1])
		if err != nil {
			return err
		}
		fmt.Println(h)
		return nil
	default:
		return usage()
	}
}

func usage() error {
	return fmt.Errorf("usage: authctl <genkey|hash <password>>")
}
