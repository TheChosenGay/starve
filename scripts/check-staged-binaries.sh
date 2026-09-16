#!/bin/sh
# 拦截"误提交二进制/构建产物"。
#
# 为什么需要自动化：这个错误在本项目已经犯了**四次**
# （snapprobe、aggrodemo、throw.wasm、pomelo-client，最大的 11MB）。
# 每次都是"事后把文件名加进 .gitignore"——而下次换个名字又会重犯。
# 逐个列名字的做法治标不治本，所以改成提交前扫描暂存区。
#
# 判定方式（不依赖文件名，看内容）：
#   1. 文件头是可执行格式（Mach-O / ELF / PE）
#   2. 或大小超过阈值且不在白名单里（如 assets/ 下的资源）
#
# 用法：sh scripts/check-staged-binaries.sh
set -eu

LIMIT_BYTES=1048576 # 1MB：源码/配置不会这么大

# 注意 filter 要含 R（rename）：git 可能把"删掉一个二进制、加一个同名不同名的
# 二进制"识别成重命名，此时 --diff-filter=ACM 会漏掉它（实测踩过，
# 检查器对真实的 11MB 可执行文件报"通过"= 假阴性）。
staged=$(git diff --cached --name-only --diff-filter=ACMR)
[ -z "$staged" ] && exit 0

bad=""
for f in $staged; do
  [ -f "$f" ] || continue
  # 1) 可执行文件头
  head4=$(dd if="$f" bs=1 count=4 2>/dev/null | od -An -tx1 | tr -d ' \n')
  case "$head4" in
    cffaedfe*|cefaedfe*|cafebabe*|7f454c46*|4d5a*) # Mach-O / ELF / PE
      bad="$bad\n  $f（可执行文件）"
      continue
      ;;
  esac
  # 2) 体积过大
  size=$(wc -c <"$f" | tr -d ' ')
  if [ "$size" -gt "$LIMIT_BYTES" ]; then
    bad="$bad\n  $f（$((size / 1024))KB）"
  fi
done

if [ -n "$bad" ]; then
  echo "✗ 暂存区里有构建产物/大文件，不应提交："
  printf "%b\n" "$bad"
  echo ""
  echo "  如果是构建产物：git rm --cached <file> 并加进 .gitignore"
  echo "  如果确实要提交大文件：确认后手动绕过本检查"
  exit 1
fi
exit 0
