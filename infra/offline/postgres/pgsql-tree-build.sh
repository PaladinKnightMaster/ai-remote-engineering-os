#!/usr/bin/env bash
# pgsql-tree-build.sh — runs INSIDE the pinned base-OS image (build-pgsql-trees.sh
# starts it). Source-builds PostgreSQL 16 into /opt/aireos/pgsql and emits a
# relocatable tree to $OUT (ADR-0077 D1-D3, T-171).
#   PHASE=all     deps -> download+verify -> configure/make/install -> bundle -> prove
#   PHASE=bundle  only the bundle+prove steps, on a tree mounted at /in (used to
#                 self-test the bundling path against a doctored reference set)
# Env: OSTAG PGSQL_VERSION PGSQL_URL PGSQL_SHA256 BASE_IMAGE REFERENCE_LIBS [PHASE OUT]
set -euo pipefail

PREFIX=/opt/aireos/pgsql
OUT="${OUT:-/out}"
PHASE="${PHASE:-all}"
: "${OSTAG:?}" "${REFERENCE_LIBS:?}" "${BASE_IMAGE:?}"
log() { echo "[pgsql-tree:$OSTAG] $*"; }
die() { echo "[pgsql-tree:$OSTAG] ERROR: $*" >&2; exit 1; }

# Configure, per ADR-0077 D2 (decided on measurement, see the T-171 report):
#   --without-icu   product SQL uses no COLLATE/ICU; full-text search is the built-in snowball.
#   --without-readline  absent from both Ubuntu bases; would be bundled for psql line editing only.
#   --with-systemd  libsystemd.so.0 is in every base; gives the unit Type=notify at zero bundling cost.
#   --with-zlib     libz.so.1 is in every base.
# Everything else optional is off. --disable-rpath drops PG's absolute rpath; ours is $ORIGIN-only.
CONFIGURE_FLAGS=(--prefix="$PREFIX" --disable-rpath --with-openssl --with-zlib --with-systemd
  --without-icu --without-readline --without-gssapi --without-pam --without-ldap --without-bonjour
  --without-bsd-auth --without-selinux --without-tcl --without-perl --without-python
  --without-libxml --without-libxslt --without-lz4 --without-zstd --without-llvm)

install_deps() {
  if command -v apt-get >/dev/null 2>&1; then
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq
    local pk="binutils jq patchelf"
    [ "$PHASE" = all ] && pk="$pk build-essential perl pkg-config curl ca-certificates bzip2 libssl-dev zlib1g-dev libsystemd-dev"
    apt-get install -y -qq --no-install-recommends $pk >/dev/null
  elif command -v dnf >/dev/null 2>&1; then
    local pk="binutils jq"
    [ "$PHASE" = all ] && pk="$pk gcc make perl bzip2 diffutils openssl-devel zlib-devel systemd-devel"
    dnf install -y -q $pk >/dev/null
  else
    die "need apt-get or dnf"
  fi
}

build() {
  local w=/tmp/pgbuild; mkdir -p "$w"; cd "$w"
  log "download $PGSQL_URL"
  curl -fsSL --retry 3 -o pg.tar.bz2 "$PGSQL_URL"
  local got; got="$(sha256sum pg.tar.bz2 | cut -d' ' -f1)"
  # Checksum BEFORE extract: a mismatch must never reach tar or configure.
  [ "$got" = "$PGSQL_SHA256" ] || die "source tarball sha256 mismatch: want $PGSQL_SHA256 got $got — refusing to extract"
  log "sha256 verified ($got)"
  tar -xjf pg.tar.bz2
  cd "postgresql-$PGSQL_VERSION"
  log "==> configure ${CONFIGURE_FLAGS[*]}"
  ./configure "${CONFIGURE_FLAGS[@]}" >/tmp/configure.log 2>&1 || { tail -30 /tmp/configure.log >&2; die "configure failed"; }
  # $$ORIGIN inside single quotes survives make then the shell as a literal $ORIGIN.
  # --disable-new-dtags => DT_RPATH, which (unlike RUNPATH) also covers transitive deps.
  # bin/ uses ../lib; lib/ (libpq + pgcrypto.so share one dir, since the prefix contains "pgsql") uses $ORIGIN.
  local ldf=(LDFLAGS_EX="-Wl,-rpath,'\$\$ORIGIN/../lib:\$\$ORIGIN' -Wl,--disable-new-dtags"
             LDFLAGS_SL="-Wl,-rpath,'\$\$ORIGIN/../lib:\$\$ORIGIN' -Wl,--disable-new-dtags")
  log "==> make"
  make -s -j"$(nproc)" -C src "${ldf[@]}" >/tmp/make.log 2>&1 || { tail -30 /tmp/make.log >&2; die "make failed"; }
  make -s -j"$(nproc)" -C contrib/pgcrypto "${ldf[@]}" >>/tmp/make.log 2>&1 || { tail -30 /tmp/make.log >&2; die "pgcrypto build failed"; }
  make -s -C src "${ldf[@]}" install >>/tmp/make.log 2>&1 && make -s -C contrib/pgcrypto "${ldf[@]}" install >>/tmp/make.log 2>&1 \
    || { tail -30 /tmp/make.log >&2; die "make install failed"; }
  # No docs, headers, static libs, ecpg, pgxs (extension build kit) or pg_config (embeds build paths/flags): not used by the product.
  rm -rf "$PREFIX"/{include,share/doc,share/man} "$PREFIX"/lib/pkgconfig "$PREFIX"/lib/pgxs "$PREFIX"/lib/*.a \
         "$PREFIX"/bin/ecpg "$PREFIX"/bin/pg_config "$PREFIX"/lib/libecpg* "$PREFIX"/lib/libpgtypes*
}

is_elf() { [ "$(od -An -tx1 -N4 "$1" 2>/dev/null | tr -d ' \n')" = 7f454c46 ]; }
needed() { readelf -d "$1" | sed -n 's/.*(NEEDED).*\[\(.*\)\]/\1/p'; }
# ADR-0077 D3: the glibc family is never bundled, whatever the measurement says.
is_glibc() { case "$1" in libc.so.*|libm.so.*|libdl.so.*|libpthread.so.*|librt.so.*|libresolv.so.*|libutil.so.*|libanl.so.*|ld-linux*|ld64*) return 0;; esac; return 1; }
in_ref() { grep -qxF "$1" "$REFERENCE_LIBS"; }
all_elfs() { find "$PREFIX" -type f | while read -r f; do is_elf "$f" && echo "$f"; done; true; }

# patchelf gives a bundled lib its own $ORIGIN rpath. Ubuntu: installed with the deps. Rocky: only in EPEL,
# so it is fetched lazily — a normal build (nothing bundled) never adds that repository.
need_patchelf() {
  command -v patchelf >/dev/null 2>&1 && return 0
  { dnf install -y -q epel-release && dnf install -y -q patchelf; } >/dev/null 2>&1 || true
  command -v patchelf >/dev/null 2>&1 || die "bundling needs patchelf (to give the library an \$ORIGIN rpath) and it is not installable"
}

pkg_of() {   # path -> "name<TAB>version<TAB>arch"; dies if the source package is unknown
  local p="$1" c n
  if command -v dpkg-query >/dev/null 2>&1; then
    for c in "$p" "$(readlink -f "$p")" "/usr$p" "${p#/usr}"; do
      n="$(dpkg-query -S "$c" 2>/dev/null | head -1 | cut -d: -f1)" && [ -n "$n" ] || continue
      dpkg-query -W -f='${Package}\t${Version}\t${Architecture}\n' "$n" | head -1; return 0
    done
  else
    rpm -qf --qf '%{NAME}\t%{VERSION}-%{RELEASE}\t%{ARCH}\n' "$(readlink -f "$p")" 2>/dev/null | head -1 | grep . && return 0
  fi
  die "no owning package for bundled library $p"
}

bundle() {
  local share="$PREFIX/share/aireos"; mkdir -p "$share"
  local hostf=/tmp/host.txt bund=/tmp/bundled.tsv; : >"$hostf"; : >"$bund"
  local -a queue; mapfile -t queue < <(all_elfs)
  local f s src pk
  while [ "${#queue[@]}" -gt 0 ]; do     # fixpoint: bundled libs' own deps are classified too
    f="${queue[0]}"; queue=("${queue[@]:1}")
    for s in $(needed "$f"); do
      if [ -e "$PREFIX/lib/$s" ]; then continue                       # ours (libpq) or already bundled
      elif in_ref "$s"; then echo "$s" >>"$hostf"                      # present in the pinned base => host-linked
      elif is_glibc "$s"; then die "$s (glibc family) is not in the base image's ldconfig cache"
      else
        src="$(ldconfig -p | awk -v s="$s" '$1==s && /x86-64/ {print $NF; exit}')"
        [ -n "$src" ] || die "$s needed by $f is neither in the base image nor in the build container"
        pk="$(pkg_of "$src")"      # assignment, so a failure aborts (die inside $(...) alone would not)
        log "BUNDLE $s <- $src  (package: $(cut -f1,2 <<<"$pk" | tr '\t' ' '))"
        cp -L "$src" "$PREFIX/lib/$s"; chmod 755 "$PREFIX/lib/$s"
        need_patchelf
        patchelf --remove-rpath "$PREFIX/lib/$s"; patchelf --force-rpath --set-rpath '$ORIGIN' "$PREFIX/lib/$s"
        printf '%s\t%s\t%s\t%s\t%s\n' "$s" "$(sha256sum "$PREFIX/lib/$s" | cut -d' ' -f1)" \
          "$(sha256sum "$(readlink -f "$src")" | cut -d' ' -f1)" "$src" "$pk" >>"$bund"
        queue+=("$PREFIX/lib/$s")
      fi
    done
  done
  # Strip our own ELFs only; bundled libs stay as shipped by their package (+ rpath).
  all_elfs | while read -r f; do awk -F'\t' -v n="$(basename "$f")" '$1==n{f=1} END{exit !f}' "$bund" || strip --strip-unneeded "$f" || true; done
  sort -u "$hostf" >"$share/HOST_LIBS"
  cut -f1 "$bund" | sort -u >"$share/BUNDLED_LIBS"
  jq -n --arg os "$OSTAG" --arg img "$BASE_IMAGE" --arg v "$PGSQL_VERSION" --arg u "$PGSQL_URL" \
    --arg h "$PGSQL_SHA256" --arg cf "${CONFIGURE_FLAGS[*]}" --arg pfx "$PREFIX" \
    --rawfile hl "$share/HOST_LIBS" --rawfile bt "$bund" \
    '{schema:1, ostag:$os, base_image:$img, prefix:$pfx,
      postgres:{version:$v, source_url:$u, sha256:$h, configure_flags:$cf, contrib:["pgcrypto"]},
      bundled:($bt|split("\n")|map(select(length>0)|split("\t")|
        {soname:.[0], sha256:.[1], source_sha256:.[2], source_path:.[3], package:.[4], package_version:.[5], package_arch:.[6]})),
      host_libs:($hl|split("\n")|map(select(length>0)))}' >"$share/COMPONENTS.json"
  jq -e . "$share/COMPONENTS.json" >/dev/null || die "COMPONENTS.json is not valid JSON"
}

prove() {   # D3 relocatable linking: every ELF carries an $ORIGIN-only DT_RPATH; nothing absolute, no RUNPATH, no build dir
  local n=0 f rp bad=0
  while read -r f; do
    n=$((n+1)); rp="$(readelf -d "$f" | sed -n 's/.*(RPATH).*\[\(.*\)\]/\1/p')"
    if readelf -d "$f" | grep -q '(RUNPATH)'; then echo "RUNPATH present: $f" >&2; bad=1; fi
    if [ -z "$rp" ] || [ -n "$(tr ':' '\n' <<<"$rp" | grep -v '^\$ORIGIN' || true)" ]; then echo "bad/absent RPATH '${rp}': $f" >&2; bad=1; fi
  done < <(all_elfs)
  [ "$bad" = 0 ] || die "rpath proof failed"
  local leak; leak="$(grep -rlaF /tmp/pgbuild "$PREFIX" 2>/dev/null || true)"
  [ -z "$leak" ] || die "build-directory path leaked into: $leak"
  log "rpath proof: $n ELF files, all DT_RPATH=\$ORIGIN-relative, no RUNPATH, no build-dir strings"
}

install_deps
if [ "$PHASE" = all ]; then
  : "${PGSQL_VERSION:?}" "${PGSQL_URL:?}" "${PGSQL_SHA256:?}"
  build
else
  mkdir -p "$PREFIX"; cp -a /in/. "$PREFIX/"; rm -rf "$PREFIX/share/aireos"
fi
bundle
prove
mkdir -p "$OUT"; cp -a "$PREFIX"/. "$OUT"/
log "tree: $(du -sh "$PREFIX" | cut -f1); bundled: $(wc -l <"$PREFIX/share/aireos/BUNDLED_LIBS"); host libs: $(wc -l <"$PREFIX/share/aireos/HOST_LIBS")"
