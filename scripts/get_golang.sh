#/bin/sh

set -e

GO_TARBALL="go1.23.2.linux-amd64.tar.gz"
GO_SAVE_PATH="$GO_BASE_PATH"
EXPECTED_HASH="542d3c1705f1c6a1c5a80d5dc62e2e45171af291e755d591c5e6531ef63b454e"

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
