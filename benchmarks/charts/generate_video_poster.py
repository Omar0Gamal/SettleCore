"""Generate a studio 2560x1480 clickable video player poster card for README.md."""

from __future__ import annotations

import argparse
from pathlib import Path

from PIL import Image, ImageDraw, ImageFilter, ImageFont


def _load_font(candidates: list[str], size: int) -> ImageFont.FreeTypeFont | ImageFont.ImageFont:
    for path in candidates:
        font_path = Path(path)
        if font_path.exists():
            return ImageFont.truetype(str(font_path), size=size)
    return ImageFont.load_default()


def build_video_poster(src_path: Path, dst_path: Path) -> None:
    """Compose frosted glass play badge and subtle player timebar onto terminal hero PNG."""
    base = Image.open(src_path).convert("RGBA")
    width, height = base.size

    # Subtle radial-like vignette: darken edges and lower band gently so controls pop
    scrim = Image.new("RGBA", (width, height), (0, 0, 0, 0))
    scrim_draw = ImageDraw.Draw(scrim)
    scrim_draw.rectangle([0, 0, width, height], fill=(7, 10, 14, 52))

    # Soft bottom gradient inside the browser viewport for timebar legibility
    for offset in range(220):
        alpha = int(125 * ((offset / 220.0) ** 1.6))
        y = height - 220 + offset
        scrim_draw.line([(0, y), (width, y)], fill=(6, 9, 13, alpha))

    composed = Image.alpha_composite(base, scrim)

    # Fonts
    sans_bold = _load_font(
        [
            r"C:\Windows\Fonts\segoeuib.ttf",
            r"C:\Windows\Fonts\arialbd.ttf",
            "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf",
        ],
        size=32,
    )
    sans_sub = _load_font(
        [
            r"C:\Windows\Fonts\segoeui.ttf",
            r"C:\Windows\Fonts\arial.ttf",
            "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
        ],
        size=21,
    )
    mono_font = _load_font(
        [
            r"C:\Windows\Fonts\consolab.ttf",
            r"C:\Windows\Fonts\consola.ttf",
            "/usr/share/fonts/truetype/dejavu/DejaVuSansMono-Bold.ttf",
        ],
        size=20,
    )

    # --- Center Glassmorphic Play Badge ---
    headline = "Watch 60fps Full-Stack Walkthrough (0:50)"
    subtitle = "1080p MP4  ·  C++17 CLOB  ·  3-Block Reorg Drill  ·  Merkle Proof-of-Reserves"
    probe_draw = ImageDraw.Draw(composed)
    h_box = probe_draw.textbbox((0, 0), headline, font=sans_bold)
    s_box = probe_draw.textbbox((0, 0), subtitle, font=sans_sub)
    content_w = max(h_box[2] - h_box[0], s_box[2] - s_box[0])

    pill_h = 128
    pill_w = 136 + content_w + 54
    pill_x0 = (width - pill_w) // 2
    pill_y0 = (height - pill_h) // 2 - 20
    pill_x1 = pill_x0 + pill_w
    pill_y1 = pill_y0 + pill_h
    pill_r = pill_h // 2

    # Drop shadow behind pill
    shadow_layer = Image.new("RGBA", (width, height), (0, 0, 0, 0))
    shadow_draw = ImageDraw.Draw(shadow_layer)
    shadow_draw.rounded_rectangle(
        [pill_x0 - 8, pill_y0 + 10, pill_x1 + 8, pill_y1 + 26],
        radius=pill_r + 8,
        fill=(0, 0, 0, 175),
    )
    shadow_layer = shadow_layer.filter(ImageFilter.GaussianBlur(radius=28))
    composed = Image.alpha_composite(composed, shadow_layer)

    # Frosted backdrop blur clipped to pill shape
    crop_box = (pill_x0, pill_y0, pill_x1, pill_y1)
    blurred_region = composed.crop(crop_box).filter(ImageFilter.GaussianBlur(radius=22))
    pill_mask = Image.new("L", (pill_w, pill_h), 0)
    ImageDraw.Draw(pill_mask).rounded_rectangle(
        [0, 0, pill_w - 1, pill_h - 1],
        radius=pill_r,
        fill=255,
    )
    composed.paste(blurred_region, (pill_x0, pill_y0), pill_mask)

    # Translucent glass fill + specular border
    glass_overlay = Image.new("RGBA", (width, height), (0, 0, 0, 0))
    glass_draw = ImageDraw.Draw(glass_overlay)
    glass_draw.rounded_rectangle(
        [pill_x0, pill_y0, pill_x1, pill_y1],
        radius=pill_r,
        fill=(13, 17, 23, 206),
        outline=(56, 189, 248, 170),
        width=2,
    )
    # Subtle inner top highlight
    glass_draw.rounded_rectangle(
        [pill_x0 + 2, pill_y0 + 2, pill_x1 - 2, pill_y1 - 2],
        radius=pill_r - 2,
        outline=(255, 255, 255, 36),
        width=1,
    )

    # Circular Play Icon Badge on left side of pill
    circle_d = 84
    cx = pill_x0 + 24 + circle_d // 2
    cy = pill_y0 + pill_h // 2
    glass_draw.ellipse(
        [cx - circle_d // 2, cy - circle_d // 2, cx + circle_d // 2, cy + circle_d // 2],
        fill=(16, 185, 129, 245),
        outline=(110, 231, 183, 210),
        width=2,
    )
    # Crisp right-pointing triangle inside circle
    tri_offset_x = 4
    tri_pts = [
        (cx - 12 + tri_offset_x, cy - 18),
        (cx - 12 + tri_offset_x, cy + 18),
        (cx + 20 + tri_offset_x, cy),
    ]
    glass_draw.polygon(tri_pts, fill=(11, 14, 17, 255))

    # Pill headline + subtle subtitle
    text_x = pill_x0 + 132
    glass_draw.text(
        (text_x, pill_y0 + 28),
        headline,
        font=sans_bold,
        fill=(240, 246, 252, 255),
    )
    glass_draw.text(
        (text_x, pill_y0 + 74),
        subtitle,
        font=sans_sub,
        fill=(148, 163, 184, 245),
    )

    composed = Image.alpha_composite(composed, glass_overlay)

    # --- Subtle Bottom Player Scrubber Timebar ---
    bar_margin_x = 160
    bar_h = 56
    bar_x0 = bar_margin_x
    bar_x1 = width - bar_margin_x
    bar_y1 = height - 96
    bar_y0 = bar_y1 - bar_h
    bar_w = bar_x1 - bar_x0

    bar_blur = composed.crop((bar_x0, bar_y0, bar_x1, bar_y1)).filter(
        ImageFilter.GaussianBlur(radius=16)
    )
    bar_mask = Image.new("L", (bar_w, bar_h), 0)
    ImageDraw.Draw(bar_mask).rounded_rectangle(
        [0, 0, bar_w - 1, bar_h - 1],
        radius=14,
        fill=255,
    )
    composed.paste(bar_blur, (bar_x0, bar_y0), bar_mask)

    bar_overlay = Image.new("RGBA", (width, height), (0, 0, 0, 0))
    bar_draw = ImageDraw.Draw(bar_overlay)
    bar_draw.rounded_rectangle(
        [bar_x0, bar_y0, bar_x1, bar_y1],
        radius=14,
        fill=(11, 14, 17, 215),
        outline=(255, 255, 255, 34),
        width=1,
    )

    # Mini play icon inside bar
    mini_cx = bar_x0 + 30
    mini_cy = bar_y0 + bar_h // 2
    bar_draw.polygon(
        [(mini_cx - 6, mini_cy - 8), (mini_cx - 6, mini_cy + 8), (mini_cx + 8, mini_cy)],
        fill=(240, 246, 252, 235),
    )

    # Timestamp & resolution pill on right
    meta_text = "0:00 / 0:50  ·  1080p 60fps"
    meta_bbox = bar_draw.textbbox((0, 0), meta_text, font=mono_font)
    meta_w = meta_bbox[2] - meta_bbox[0]
    meta_x = bar_x1 - meta_w - 28
    bar_draw.text(
        (meta_x, bar_y0 + 17),
        meta_text,
        font=mono_font,
        fill=(203, 213, 225, 240),
    )

    # Progress track
    track_x0 = bar_x0 + 62
    track_x1 = meta_x - 28
    track_cy = bar_y0 + bar_h // 2
    bar_draw.rounded_rectangle(
        [track_x0, track_cy - 3, track_x1, track_cy + 3],
        radius=3,
        fill=(51, 65, 85, 200),
    )
    progress_x = track_x0 + int((track_x1 - track_x0) * 0.24)
    bar_draw.rounded_rectangle(
        [track_x0, track_cy - 3, progress_x, track_cy + 3],
        radius=3,
        fill=(56, 189, 248, 255),
    )
    bar_draw.ellipse(
        [progress_x - 7, track_cy - 7, progress_x + 7, track_cy + 7],
        fill=(16, 185, 129, 255),
        outline=(240, 246, 252, 255),
        width=2,
    )

    composed = Image.alpha_composite(composed, bar_overlay)

    dst_path.parent.mkdir(parents=True, exist_ok=True)
    composed.convert("RGB").save(dst_path, format="PNG", optimize=True)


def main() -> None:
    parser = argparse.ArgumentParser(description="Generate SettleCore video poster card PNG.")
    parser.add_argument(
        "--src",
        type=Path,
        default=Path("../../docs/assets/media/settlecore-trading-terminal.png"),
    )
    parser.add_argument(
        "--dst",
        type=Path,
        default=Path("../../docs/assets/media/settlecore-video-poster.png"),
    )
    args = parser.parse_args()
    build_video_poster(args.src.resolve(), args.dst.resolve())


if __name__ == "__main__":
    main()
