const fs = require('node:fs');
const path = require('node:path');
const zlib = require('node:zlib');
const { execFileSync } = require('node:child_process');

const root = path.resolve(__dirname, '..');
const buildDir = path.join(root, 'build');
const iconsetDir = path.join(buildDir, 'Outpost.iconset');

fs.mkdirSync(buildDir, { recursive: true });

writePng(path.join(buildDir, 'icon.png'), 512, 512, drawAppIcon);
writePng(path.join(buildDir, 'trayTemplate.png'), 36, 36, drawTrayIcon);
createIconset();

function createIconset() {
  fs.rmSync(iconsetDir, { recursive: true, force: true });
  fs.mkdirSync(iconsetDir, { recursive: true });

  const source = path.join(buildDir, 'icon.png');
  const sizes = [
    [16, 'icon_16x16.png'],
    [32, 'icon_16x16@2x.png'],
    [32, 'icon_32x32.png'],
    [64, 'icon_32x32@2x.png'],
    [128, 'icon_128x128.png'],
    [256, 'icon_128x128@2x.png'],
    [256, 'icon_256x256.png'],
    [512, 'icon_256x256@2x.png'],
    [512, 'icon_512x512.png'],
  ];

  for (const [size, filename] of sizes) {
    execFileSync('sips', ['-z', String(size), String(size), source, '--out', path.join(iconsetDir, filename)], {
      stdio: 'ignore',
    });
  }

  execFileSync('iconutil', ['-c', 'icns', iconsetDir, '-o', path.join(buildDir, 'icon.icns')], {
    stdio: 'ignore',
  });
  fs.rmSync(iconsetDir, { recursive: true, force: true });
}

function drawAppIcon(x, y, width, height) {
  const radius = width * 0.21;
  const dx = x - width / 2;
  const dy = y - height / 2;
  const distance = Math.hypot(dx, dy);

  if (distance > width * 0.46) return [0, 0, 0, 0];

  const bg = mix([20, 30, 26], [37, 107, 85], y / height);
  let color = bg;

  if (Math.abs(dx) < width * 0.055 && Math.abs(dy) < width * 0.24) {
    color = [239, 246, 240];
  }
  if (Math.abs(dy) < width * 0.055 && Math.abs(dx) < width * 0.24) {
    color = [239, 246, 240];
  }
  if (distance < radius) {
    color = [68, 178, 130];
  }
  if (distance < radius * 0.48) {
    color = [244, 246, 243];
  }

  return [...color, 255];
}

function drawTrayIcon(x, y, width, height) {
  const dx = x - width / 2;
  const dy = y - height / 2;
  const distance = Math.hypot(dx, dy);
  const alpha = distance > width * 0.42 ? 0 : 255;
  if (!alpha) return [0, 0, 0, 0];

  if (Math.abs(dx) < width * 0.07 && Math.abs(dy) < width * 0.25) {
    return [0, 0, 0, 255];
  }
  if (Math.abs(dy) < width * 0.07 && Math.abs(dx) < width * 0.25) {
    return [0, 0, 0, 255];
  }
  if (distance < width * 0.14) {
    return [0, 0, 0, 255];
  }

  return [0, 0, 0, 0];
}

function writePng(file, width, height, painter) {
  const raw = Buffer.alloc((width * 4 + 1) * height);

  for (let y = 0; y < height; y += 1) {
    const row = y * (width * 4 + 1);
    raw[row] = 0;
    for (let x = 0; x < width; x += 1) {
      const [r, g, b, a] = painter(x + 0.5, y + 0.5, width, height);
      const offset = row + 1 + x * 4;
      raw[offset] = r;
      raw[offset + 1] = g;
      raw[offset + 2] = b;
      raw[offset + 3] = a;
    }
  }

  const chunks = [
    chunk('IHDR', Buffer.concat([u32(width), u32(height), Buffer.from([8, 6, 0, 0, 0])])),
    chunk('IDAT', zlib.deflateSync(raw)),
    chunk('IEND', Buffer.alloc(0)),
  ];

  fs.writeFileSync(file, Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), ...chunks]));
}

function chunk(type, data) {
  const typeBuffer = Buffer.from(type);
  return Buffer.concat([u32(data.length), typeBuffer, data, u32(crc32(Buffer.concat([typeBuffer, data])))]);
}

function u32(value) {
  const buffer = Buffer.alloc(4);
  buffer.writeUInt32BE(value >>> 0);
  return buffer;
}

function crc32(buffer) {
  let crc = 0xffffffff;
  for (const byte of buffer) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit += 1) {
      crc = crc & 1 ? 0xedb88320 ^ (crc >>> 1) : crc >>> 1;
    }
  }
  return (crc ^ 0xffffffff) >>> 0;
}

function mix(a, b, amount) {
  return a.map((value, index) => Math.round(value + (b[index] - value) * amount));
}
