from pathlib import Path
import re, sys

root = Path(__file__).resolve().parents[1]
index = root / "docs" / "index.html"
text = index.read_text(encoding="utf-8")
srcs = re.findall(r'<img\b[^>]*\bsrc=["\']([^"\']+)["\']', text, flags=re.I)

missing = []
for src in srcs:
    if "://" in src or src.startswith("data:"):
        continue
    p = (index.parent / src).resolve()
    if not p.is_file():
        missing.append((src, p))

if missing:
    print("Missing image assets:")
    for src, p in missing:
        print(f"  - {src} -> {p}")
    sys.exit(1)

print(f"OK: {len(srcs)} image reference(s) resolved.")
