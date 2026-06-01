#!/bin/sh
# Entry point for the T-020b test sshd. Waits for the seed one-shot to deliver
# the runtime-generated host key + authorized client key on the shared
# sshd-keys volume, installs them, then runs sshd (pubkey-only, user `aireos`,
# port 2222). The labels.ssh_host_key on the seeded resource pins THIS host key
# (broker ssh.FixedHostKey), and the encrypted connection_secret is the client
# key whose public half lands in authorized_keys here.
set -e

KEYS=/seed-keys
echo "sshd: waiting for seed-provisioned keys on ${KEYS} ..."
i=0
while [ ! -s "${KEYS}/ssh_host_ed25519_key" ] || [ ! -s "${KEYS}/authorized_keys" ]; do
  i=$((i + 1))
  if [ "${i}" -gt 60 ]; then
    echo "sshd: timed out waiting for seed keys" >&2
    exit 1
  fi
  sleep 1
done

install -d -m 755 /etc/ssh
install -m 600 "${KEYS}/ssh_host_ed25519_key" /etc/ssh/ssh_host_ed25519_key
install -d -m 700 -o aireos -g aireos /home/aireos/.ssh
install -m 600 -o aireos -g aireos "${KEYS}/authorized_keys" /home/aireos/.ssh/authorized_keys

cat > /etc/ssh/sshd_config <<EOF
Port 2222
HostKey /etc/ssh/ssh_host_ed25519_key
PubkeyAuthentication yes
PasswordAuthentication no
PermitRootLogin no
AllowUsers aireos
AuthorizedKeysFile .ssh/authorized_keys
EOF

echo "sshd: starting on :2222 (user aireos, pubkey-only)"
exec /usr/sbin/sshd -D -e -f /etc/ssh/sshd_config
