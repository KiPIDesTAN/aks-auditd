#/bin/sh

set -e

GO_TARBALL="go1.23.1.linux-amd64.tar.gz"
GO_SAVE_PATH="$GO_BASE_PATH"
EXPECTED_HASH="49bbb517cfa9eee677e1e7897f7cf9cfdbcf49e05f61984a2789136de359f9bd"

cd $GO_SAVE_PATH

# 1. Download the go tarball for linux amd64
curl -LO "https://go.dev/dl/$GO_TARBALL" 

# 2. Verify the hash of the tarball
COMPUTED_HASH=$(sha256sum "$GO_TARBALL" | cut -d ' ' -f 1)

if [ "$COMPUTED_HASH" != "$EXPECTED_HASH" ]; then
  echo "Hash does not match"
  return 1
fi

tar vfzx $GO_TARBALL
