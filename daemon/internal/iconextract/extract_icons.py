#!/usr/bin/env python3
import sys

def main() -> int:
    if len(sys.argv) != 3:
        print("ERROR usage: extract_icons.py <bundle_path> <out_dir>")
        return 2

    bundle_path, out_dir = sys.argv[1], sys.argv[2]

    try:
        import UnityPy
    except ImportError as e:
        print(f"ERROR UnityPy не установлен в этом Python-окружении: {e}")
        return 3

    import os
    os.makedirs(out_dir, exist_ok=True)

    try:
        env = UnityPy.load(bundle_path)
    except Exception as e:
        print(f"ERROR не удалось открыть бандл {bundle_path}: {e}")
        return 4

    ok, failed = 0, 0
    for obj in env.objects:
        if obj.type.name != "Texture2D":
            continue
        try:
            data = obj.read()
            name = data.m_Name
            if not name:
                continue
            img = data.image
            if img.mode != "RGBA":
                img = img.convert("RGBA")
            img.save(os.path.join(out_dir, f"{name}.png"))
            ok += 1
        except Exception:
            failed += 1

    print(f"OK {ok} failed={failed}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
