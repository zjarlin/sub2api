#!/bin/sh
set -eu

model_dir=${HYMT_MODEL_DIR:-/opt/sub2api/models/hy-mt2}
hub=${HYMT_MODEL_HUB:-https://huggingface.co}
revision=a0c709d9fac510f2c807aa3af52872340dc37a4a
file=Hy-MT2-1.8B-Q8_0.gguf
sha=5c3fe0b1408a5ceb0143184ef247b11b579c525f4b02b060e6c851bb76fef1a4

mkdir -p "$model_dir"
if [ -f "$model_dir/$file" ] && printf '%s  %s\n' "$sha" "$model_dir/$file" | sha256sum -c -; then
  exit 0
fi
if [ -f "$model_dir/$file.part" ] && printf '%s  %s\n' "$sha" "$model_dir/$file.part" | sha256sum -c -; then
  mv "$model_dir/$file.part" "$model_dir/$file"
  exit 0
fi

curl -fL --connect-timeout 15 --max-time 1800 --retry 3 \
  -o "$model_dir/$file.part" "$hub/tencent/Hy-MT2-1.8B-GGUF/resolve/$revision/$file"
printf '%s  %s\n' "$sha" "$model_dir/$file.part" | sha256sum -c -
mv "$model_dir/$file.part" "$model_dir/$file"
printf 'Hy-MT2 model verified: %s\n' "$model_dir/$file"
