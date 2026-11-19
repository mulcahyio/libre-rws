"""Apply one mutant: replace each OLD with NEW once in FILE.

usage: replace.py FILE OLD NEW [OLD2 NEW2 ...]
Exit status 1 when any OLD is not found (the file is then left unchanged).
"""
import sys

path, pairs = sys.argv[1], sys.argv[2:]
if len(pairs) % 2:
    sys.exit("odd number of replacement arguments")
with open(path, encoding="utf-8") as f:
    text = f.read()
for old, new in zip(pairs[::2], pairs[1::2]):
    if old not in text:
        sys.exit(1)
    text = text.replace(old, new, 1)
with open(path, "w", encoding="utf-8") as f:
    f.write(text)
