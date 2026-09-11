#!/bin/bash
# ---------------------------------------------------------------------------
# fix-permissions.sh — بازگرداندن بیت اجرا
#
# چرا لازم است: بیت اجرا یک خصوصیت فایل است که Git فقط اگر در ایندکس ثبت
# شده باشد نگه می‌دارد. اگر فایل‌ها یک بار بدون مجوز اجرا commit شوند، هر
# clone بعدی آنها را ۶۴۴ می‌گیرد و «Permission denied» یا در مورد sudo
# پیام گمراه‌کننده «command not found» می‌دهد.
#
# این اسکریپت هم مجوز محلی را درست می‌کند و هم — اگر داخل مخزن Git باشید —
# آن را در خود ایندکس ثبت می‌کند تا مشکل برای همیشه تمام شود.
#
#   bash fix-permissions.sh          # فقط مجوز محلی
#   bash fix-permissions.sh --git    # ثبت در ایندکس Git هم انجام شود
# ---------------------------------------------------------------------------
set -e
cd "$(dirname "$0")"

FILES=$(find . -type f \( -name '*.sh' -o -path './scripts/builders/*/bin/*' \) -not -path './.git/*')

for f in $FILES; do chmod +x "$f"; done
echo "بیت اجرا روی $(echo "$FILES" | wc -l) فایل تنظیم شد"

if [ "$1" = "--git" ]; then
  command -v git >/dev/null || { echo "git نصب نیست"; exit 1; }
  git rev-parse --is-inside-work-tree >/dev/null 2>&1 || { echo "اینجا مخزن Git نیست"; exit 1; }
  for f in $FILES; do git update-index --chmod=+x "$f" 2>/dev/null || true; done
  echo
  echo "در ایندکس Git ثبت شد. برای دائمی شدن:"
  echo "    git commit -m 'ثبت بیت اجرا برای اسکریپت‌ها' && git push"
fi
