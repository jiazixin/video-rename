// Generate web/public/favicon.ico from the sole logo source. Requires FFmpeg.
// Run: node scripts/generate-favicon.mjs
import {execFileSync} from 'node:child_process'
import {writeFileSync} from 'node:fs'
const source = new URL('../web/public/logo.png', import.meta.url)
const sizes = [16, 32, 48, 64, 128, 256]
// Match Workspace.vue's 8px radius on the 40px brand mark. Apply the
// transparent mask at 4x resolution before downsampling for smooth small icons.
const cornerRadiusRatio = 8 / 40
const radius = `W*${cornerRadiusRatio}`
const cornerDistance = `hypot(max(abs(X-(W-1)/2)-(W/2-${radius}),0),max(abs(Y-(H-1)/2)-(H/2-${radius}),0))`
const alpha = `alpha(X,Y)*clip(${radius}+0.5-${cornerDistance},0,1)`
const images = sizes.map(size => execFileSync('ffmpeg', ['-v','error','-i',source.pathname,'-vf',`scale=${size*4}:${size*4}:flags=lanczos,format=rgba,geq=r='r(X,Y)':g='g(X,Y)':b='b(X,Y)':a='${alpha}',scale=${size}:${size}:flags=area`,'-frames:v','1','-f','image2pipe','-c:v','png','pipe:1']))
const header = Buffer.alloc(6 + sizes.length * 16)
header.writeUInt16LE(1,2)
header.writeUInt16LE(sizes.length,4)
let offset = header.length
for (let i=0; i<sizes.length; i++) {
 const entry=6+i*16
 header[entry]=header[entry+1]=sizes[i]===256?0:sizes[i]
 header.writeUInt16LE(1,entry+4)
 header.writeUInt16LE(32,entry+6)
 header.writeUInt32LE(images[i].length,entry+8)
 header.writeUInt32LE(offset,entry+12)
 offset+=images[i].length
}
writeFileSync(new URL('../web/public/favicon.ico',import.meta.url),Buffer.concat([header,...images]))
