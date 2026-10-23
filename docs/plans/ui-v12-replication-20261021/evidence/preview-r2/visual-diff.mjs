// Diagnostics only: compares two 1440x960 PNGs, reports the differing share per
// horizontal band and the rows/columns of every non-masked difference cluster.
// Masks must stay limited to the registered copy differences.
import { readFileSync, writeFileSync } from 'node:fs';
import { inflateSync } from 'node:zlib';

function decode(path) {
  const buffer = readFileSync(path);
  let offset = 8;
  let width = 0, height = 0, colorType = 0, bitDepth = 0;
  const idat = [];
  while (offset < buffer.length) {
    const length = buffer.readUInt32BE(offset);
    const type = buffer.toString('ascii', offset + 4, offset + 8);
    const data = buffer.subarray(offset + 8, offset + 8 + length);
    if (type === 'IHDR') {
      width = data.readUInt32BE(0); height = data.readUInt32BE(4);
      bitDepth = data[8]; colorType = data[9];
      if (data[12] !== 0) throw new Error('interlaced PNG unsupported');
    } else if (type === 'IDAT') idat.push(data);
    else if (type === 'IEND') break;
    offset += 12 + length;
  }
  if (bitDepth !== 8 || (colorType !== 2 && colorType !== 6))
    throw new Error(`unsupported PNG ${bitDepth}/${colorType}`);
  const channels = colorType === 6 ? 4 : 3;
  const raw = inflateSync(Buffer.concat(idat));
  const stride = width * channels;
  const pixels = Buffer.alloc(width * height * 3);
  let previous = Buffer.alloc(stride);
  for (let y = 0; y < height; y++) {
    const filter = raw[y * (stride + 1)];
    const line = raw.subarray(y * (stride + 1) + 1, (y + 1) * (stride + 1));
    const out = Buffer.alloc(stride);
    for (let x = 0; x < stride; x++) {
      const a = x >= channels ? out[x - channels] : 0;
      const b = previous[x];
      const c = x >= channels ? previous[x - channels] : 0;
      const value = line[x];
      let result;
      switch (filter) {
        case 0: result = value; break;
        case 1: result = value + a; break;
        case 2: result = value + b; break;
        case 3: result = value + ((a + b) >> 1); break;
        case 4: {
          const p = a + b - c;
          const pa = Math.abs(p - a), pb = Math.abs(p - b), pc = Math.abs(p - c);
          result = value + (pa <= pb && pa <= pc ? a : pb <= pc ? b : c);
          break;
        }
        default: throw new Error(`bad filter ${filter}`);
      }
      out[x] = result & 0xff;
    }
    for (let x = 0; x < width; x++) {
      const s = x * channels, d = (y * width + x) * 3;
      pixels[d] = out[s]; pixels[d + 1] = out[s + 1]; pixels[d + 2] = out[s + 2];
    }
    previous = out;
  }
  return { width, height, pixels };
}

const [, , referencePath, actualPath, outputPath, masksJson] = process.argv;
const reference = decode(referencePath);
const actual = decode(actualPath);
if (reference.width !== actual.width || reference.height !== actual.height)
  throw new Error('canvas mismatch');
const masks = JSON.parse(masksJson || '[]');
const masked = (x, y) => masks.some(m =>
  x >= m.x && x < m.x + m.w && y >= m.y && y < m.y + m.h);

const { width, height } = reference;
const bandHeight = 40;
const bands = new Map();
let differing = 0, considered = 0, insideMasks = 0;
let minX = width, maxX = -1, minY = height, maxY = -1;
for (let y = 0; y < height; y++) {
  for (let x = 0; x < width; x++) {
    const i = (y * width + x) * 3;
    const delta = Math.max(
      Math.abs(reference.pixels[i] - actual.pixels[i]),
      Math.abs(reference.pixels[i + 1] - actual.pixels[i + 1]),
      Math.abs(reference.pixels[i + 2] - actual.pixels[i + 2]),
    );
    if (masked(x, y)) { if (delta > 16) insideMasks++; continue; }
    considered++;
    if (delta > 16) {
      differing++;
      const band = Math.floor(y / bandHeight) * bandHeight;
      const entry = bands.get(band) || { band, count: 0 };
      entry.count++; bands.set(band, entry);
      if (x < minX) minX = x; if (x > maxX) maxX = x;
      if (y < minY) minY = y; if (y > maxY) maxY = y;
    }
  }
}
const report = {
  kind: 'diagnostic-only', reference: referencePath, actual: actualPath,
  canvas: [width, height],
  method: 'max channel difference > 16 on 8-bit sRGB; masks limited to registered copy differences',
  masks,
  consideredPixels: considered,
  differingPixels: differing,
  differenceRatio: +(differing / considered).toFixed(8),
  differingInsideMasks: insideMasks,
  bounds: maxX < 0 ? null : { minX, minY, maxX, maxY },
  bands: [...bands.values()].sort((a, b) => b.count - a.count).slice(0, 8),
};
writeFileSync(outputPath, JSON.stringify(report, null, 2));
console.log(JSON.stringify({
  ratio: report.differenceRatio, differing, bounds: report.bounds, bands: report.bands,
}));
