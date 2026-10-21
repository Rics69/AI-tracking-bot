// Builds Savik's poses for the app: ../poses/<name>.png (1024², drawn by hand,
// not in git) → public/savik/{512,256}/<name>.webp.
//
// Poses are not trimmed: every pose keeps the same square canvas, so switching
// poses does not make Savik jump.
//
// Usage: npm run poses

import { mkdir, readdir, rm } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import sharp from "sharp";

const web = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const src = path.resolve(web, "../poses");
const out = path.join(web, "public/savik");
const sizes = [512, 256];

const files = (await readdir(src)).filter((f) => f.toLowerCase().endsWith(".png")).sort();
if (files.length === 0) {
  console.error(`no PNG poses in ${src}`);
  process.exit(1);
}

// Start clean, so a renamed or deleted pose does not linger in the build.
await rm(out, { recursive: true, force: true });
for (const size of sizes) {
  await mkdir(path.join(out, String(size)), { recursive: true });
}

let total = 0;
for (const file of files) {
  const name = path.basename(file, path.extname(file));
  for (const size of sizes) {
    const dest = path.join(out, String(size), `${name}.webp`);
    const info = await sharp(path.join(src, file))
      .resize(size, size, { fit: "contain", background: { r: 0, g: 0, b: 0, alpha: 0 } })
      .webp({ quality: 85, alphaQuality: 90, effort: 6 })
      .toFile(dest);
    total += info.size;
  }
  console.log(`✓ ${name}`);
}
console.log(`${files.length} poses × ${sizes.length} sizes, ${(total / 1024).toFixed(0)} KB total → ${path.relative(web, out)}`);
