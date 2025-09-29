#!/bin/bash
VERSION=$(git tag --sort=-version:refname | head -n1)
TARGET=$GOBIN
if [[ x"${GOBIN}" == "x"  ]]; then
  TARGET='/usr/local/bin'
fi
echo "Installing $VERSION into $TARGET"
FILE="go-install_darwin-$VERSION.tar.gz"
URL="https://github.com/xitonix/go-install/releases/download/$VERSION/$FILE"
echo "Downloading $URL"
curl $URL -L --max-redirs 1 --output $FILE --silent
echo "Extracting $FILE"
tar -zxf $FILE -C $TARGET/
echo "Deleting $FILE"
rm -rf $FILE
chmod +x $TARGET/go-install
go-install -v