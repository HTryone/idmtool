"""生成 IDMFrost 图标：冰晶盾牌 + 下载箭头。
输出 assets/idmfrost.ico（多尺寸）和 assets/idmfrost.png（256px 预览）。
"""
from PIL import Image, ImageDraw
import math
import os

OUT = os.path.dirname(os.path.abspath(__file__))


def shield_path(cx, cy, w, h):
    """盾牌轮廓：上方平、两侧直、下方收成尖。"""
    top = cy - h / 2
    bot = cy + h / 2
    left = cx - w / 2
    right = cx + w / 2
    shoulder = cy + h * 0.10
    pts = [
        (cx, top),                    # 顶点
        (right, top + h * 0.14),      # 右上
        (right, shoulder),            # 右肩
        (cx + w * 0.30, bot - h * 0.18),
        (cx, bot),                    # 底尖
        (cx - w * 0.30, bot - h * 0.18),
        (left, shoulder),             # 左肩
        (left, top + h * 0.14),       # 左上
    ]
    return pts


def draw_icon(size=1024):
    img = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)

    cx = cy = size / 2

    # 圆角方形底（冰蓝渐变手绘：多层叠加）
    pad = size * 0.055
    r = size * 0.22
    d.rounded_rectangle([pad, pad, size - pad, size - pad], radius=r,
                        fill=(22, 42, 66, 255))
    # 内发光层
    d.rounded_rectangle([pad + size * 0.012, pad + size * 0.012,
                         size - pad - size * 0.012, size - pad - size * 0.012],
                        radius=r * 0.95, outline=(58, 108, 168, 255),
                        width=int(size * 0.008))

    # 冰晶雪花（背景装饰，淡）
    def flake(px, py, rad, color, w):
        for i in range(6):
            ang = math.radians(i * 60)
            x2 = px + rad * math.cos(ang)
            y2 = py + rad * math.sin(ang)
            d.line([px, py, x2, y2], fill=color, width=w)
            # 分叉
            for sgn in (-1, 1):
                a2 = ang + sgn * math.radians(30)
                xm = px + rad * 0.55 * math.cos(ang)
                ym = py + rad * 0.55 * math.sin(ang)
                d.line([xm, ym, xm + rad * 0.35 * math.cos(a2),
                        ym + rad * 0.35 * math.sin(a2)], fill=color, width=w)

    for (fx, fy, fr) in [(size * 0.20, size * 0.20, size * 0.075),
                         (size * 0.80, size * 0.22, size * 0.060),
                         (size * 0.18, size * 0.80, size * 0.055),
                         (size * 0.82, size * 0.78, size * 0.070)]:
        flake(fx, fy, fr, (70, 120, 180, 90), max(2, int(size * 0.004)))

    # 盾牌：外层深蓝描边
    sw = size * 0.44          # 盾宽
    sh = size * 0.56          # 盾高
    pts = shield_path(cx, cy + size * 0.01, sw, sh)
    d.polygon(pts, fill=(30, 90, 160, 255), outline=(10, 30, 55, 255))
    # 内层亮蓝
    pts2 = shield_path(cx, cy + size * 0.012, sw * 0.84, sh * 0.84)
    d.polygon(pts2, fill=(64, 158, 235, 255))

    # 下载箭头（白），居中于盾内
    ay = cy - size * 0.075
    shaft_w = size * 0.062
    d.rectangle([cx - shaft_w / 2, ay - size * 0.115,
                 cx + shaft_w / 2, ay + size * 0.035], fill=(255, 255, 255, 255))
    head_w = size * 0.175
    head_h = size * 0.115
    d.polygon([(cx - head_w / 2, ay + size * 0.025),
               (cx + head_w / 2, ay + size * 0.025),
               (cx, ay + size * 0.025 + head_h)], fill=(255, 255, 255, 255))
    # 底部横线（下载到底盘）
    d.rectangle([cx - size * 0.115, ay + size * 0.145,
                 cx + size * 0.115, ay + size * 0.145 + size * 0.028],
                fill=(255, 255, 255, 255))

    # 高光（沿盾牌左上边缘的细长柔光，不是椭圆斑）
    hl = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    hd = ImageDraw.Draw(hl)
    hd.polygon(shield_path(cx, cy + size * 0.012, sw * 0.84, sh * 0.84),
               fill=(120, 200, 255, 40))
    # 用遮罩只保留左上 1/3 区域，形成斜向高光
    mask = Image.new("L", (size, size), 0)
    md = ImageDraw.Draw(mask)
    md.polygon([(0, 0), (size * 0.72, 0), (0, size * 0.72)], fill=255)
    hl.putalpha(Image.composite(hl.getchannel("A"),
                                Image.new("L", (size, size), 0), mask))
    img.alpha_composite(hl)
    return img


def main():
    big = draw_icon(1024)
    png = os.path.join(OUT, "idmfrost.png")
    big.save(png)
    print("PNG:", png, big.size)

    ico = os.path.join(OUT, "idmfrost.ico")
    sizes = [(16, 16), (24, 24), (32, 32), (48, 48), (64, 64),
             (128, 128), (256, 256)]
    big.save(ico, format="ICO", sizes=sizes)
    print("ICO:", ico)


if __name__ == "__main__":
    main()
