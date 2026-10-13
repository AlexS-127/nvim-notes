#!/usr/bin/env python3
"""Pull the text out of course files (slides .pptx, readings .pdf, Word .docx) so Claude can write notes and
question banks from them.

Usage: python3 ~/notes/ingest.py SUBJECT FOLDER_OR_FILE...

Each file becomes <notes>/<SUBJECT>/.sources/<name>.md (hidden: the viewer, the word counter and the revision
scheduler ignore it, so raw slide text never counts as your writing). Existing extractions are replaced.
PDFs use `pdftotext` when installed, else macOS PDFKit through `swift`. Prints one line per file."""

import os
import re
import shutil
import subprocess
import sys
import zipfile
from pathlib import Path

NOTES = Path(os.environ.get("NOTES_DIR") or Path.home() / "notes").expanduser()
EXTS = {".pptx", ".pdf", ".docx"}


def pptx_text(p):
    z = zipfile.ZipFile(p)
    slides = sorted((n for n in z.namelist() if re.fullmatch(r"ppt/slides/slide\d+\.xml", n)),
                    key=lambda n: int(re.findall(r"\d+", n)[0]))
    out = []
    for i, n in enumerate(slides, 1):
        xml = z.read(n).decode("utf-8", "ignore")
        lines = ["".join(re.findall(r"<a:t>(.*?)</a:t>", para, re.S)) for para in re.findall(r"<a:p[ >].*?</a:p>", xml, re.S)]
        out.append(f"## Slide {i}\n\n" + "\n".join(l for l in lines if l.strip()))
    return "\n\n".join(out)


def docx_text(p):
    xml = zipfile.ZipFile(p).read("word/document.xml").decode("utf-8", "ignore")
    return "\n".join("".join(re.findall(r"<w:t[^>]*>(.*?)</w:t>", para, re.S)) for para in re.findall(r"<w:p[ >].*?</w:p>", xml, re.S))


def pdf_text(p):
    if shutil.which("pdftotext"):
        return subprocess.run(["pdftotext", "-layout", str(p), "-"], capture_output=True, text=True).stdout
    script = ('import PDFKit\nlet a = CommandLine.arguments\n'
              'print(PDFDocument(url: URL(fileURLWithPath: a[1]))?.string ?? "")\n')
    r = subprocess.run(["swift", "-", str(p)], input=script, capture_output=True, text=True)
    return r.stdout


def unescape(t):
    for a, b in (("&amp;", "&"), ("&lt;", "<"), ("&gt;", ">"), ("&quot;", '"'), ("&apos;", "'")):
        t = t.replace(a, b)
    return t


def main(argv):
    if len(argv) < 2:
        sys.exit(__doc__)
    subject, paths = argv[0], [Path(a).expanduser() for a in argv[1:]]
    files = []
    for p in paths:
        files += sorted(f for f in p.rglob("*") if f.suffix.lower() in EXTS and not f.name.startswith(("~$", "."))) if p.is_dir() else [p]
    out = NOTES / subject / ".sources"
    out.mkdir(parents=True, exist_ok=True)
    for f in files:
        kind = f.suffix.lower()
        try:
            text = {".pptx": pptx_text, ".docx": docx_text, ".pdf": pdf_text}[kind](f)
        except (OSError, zipfile.BadZipFile) as e:
            print(f"skipped {f.name}: {e}")
            continue
        text = unescape(text).strip()
        (out / f"{f.stem}.md").write_text(f"# {f.stem}\n\nSource: {f}\n\n{text}\n", encoding="utf-8")
        print(f"{f.name}: {len(text.split())} words")


if __name__ == "__main__":
    main(sys.argv[1:])
