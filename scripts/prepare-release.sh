#!/bin/bash
set -euo pipefail

if [ "$#" -lt 2 ] || [ "$#" -gt 3 ]; then
  echo "用法: $0 <emby-version> <packaging-revision> [rc|stable]" >&2
  exit 1
fi

version="$1"
revision="$2"
release_channel="${3:-rc}"
readonly PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly MANIFEST="${PROJECT_ROOT}/packaging/emby/manifest"

case "${version}" in
  ''|*[!0-9.]*|.*|*.)
    echo "无效的 Emby 版本: ${version}" >&2
    exit 1
    ;;
esac
case "${revision}" in
  ''|*[!0-9]*)
    echo "无效的打包修订号: ${revision}" >&2
    exit 1
    ;;
esac
case "${release_channel}" in
  rc)
    package_version="${version}-rc${revision}"
    changelog="Emby Server ${version}；fnOS 打包修订 rc${revision}。"
    ;;
  stable)
    package_version="${version}"
    changelog="Emby Server ${version}；fnOS 正式版。"
    ;;
  *)
    echo "无效的发布类型: ${release_channel}" >&2
    exit 1
    ;;
esac

sed -i.bak -E "s/^version([[:space:]]*)=.*/version\\1= ${package_version}/" "${MANIFEST}"
sed -i.bak -E "s/^changelog([[:space:]]*)=.*/changelog\\1= ${changelog}/" "${MANIFEST}"
find "${PROJECT_ROOT}/packaging/emby" -name '*.bak' -delete
echo "已准备 Emby ${package_version}。"
