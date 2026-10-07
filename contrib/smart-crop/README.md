# Contextual crop reference

The production copy is bundled in JAVBeacon `internal/covers/cropper`, including the MIT-licensed YuNet model. Stash Metadata calls its authenticated API; this directory is a standalone reference/preview tool with matching geometry tests.

Install `requirements.txt`, download the official OpenCV YuNet 2023mar model, then run:

```sh
python yunet_crop.py original.jpg preview.jpg --model face_detection_yunet_2023mar.onnx
python -m unittest discover -s . -q
```

The output is 1200×1800. A fitting face with context wins over an oversized close-up; a tight source returns `changed: false` without creating an output. See ../../POSTER_LAYOUTS.md for deployment and repair behavior.
