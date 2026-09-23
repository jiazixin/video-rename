// Isolated browser-test fixture: never reads or mutates user media or configuration.
import { mkdtempSync,mkdirSync,writeFileSync,rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join,resolve } from 'node:path'
import { spawn,execFileSync } from 'node:child_process'
import { createServer } from 'node:http'
const base=mkdtempSync(join(tmpdir(),'video-rename-e2e-'))
const media=join(base,'media');mkdirSync(media)
for(const dir of ['2026 · 旅行记录','家庭影像','工作素材','纪录片收藏','待整理视频','自动命名测试','分页测试','取消测试'])mkdirSync(join(media,dir))
for(let i=0;i<160;i++)mkdirSync(join(media,'返回定位测试',`目录_${String(i).padStart(3,'0')}`),{recursive:true})
writeFileSync(join(media,'VID_20260920_001.mp4'),'fixture')
for(const imageName of ['photo.png','photo2.png','photo3.png','photo4.png','photo5.png'])writeFileSync(join(media,imageName),Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aX1sAAAAASUVORK5CYII=','base64'))
writeFileSync(join(media,'已占用名称.mp4'),'conflict fixture')
writeFileSync(join(media,'2026 · 旅行记录','IMG_20260901.mp4'),'nested fixture')
for(let i=0;i<230;i++)writeFileSync(join(media,'分页测试',`片段_${String(i).padStart(4,'0')}.mp4`),'fixture')
execFileSync('ffmpeg',['-v','error','-f','lavfi','-i','color=c=green:s=320x240:r=10','-t','3','-c:v','mpeg4',join(media,'自动命名测试','clip.mp4')])
writeFileSync(join(media,'取消测试','pending.mp4'),'cancellation fixture')
execFileSync('ffmpeg',['-v','error','-f','lavfi','-i','color=c=green:s=320x240:r=10','-t','30','-c:v','libvpx',join(media,'preview.webm')])
const numberingDir=join(media,'图片编号测试');mkdirSync(numberingDir)
for(const name of ['1.JPG','1.PNG','3.WEBP',...Array.from({length:202},(_,i)=>`photo-${String(i).padStart(3,'0')}.PNG`)])writeFileSync(join(numberingDir,name),name)
writeFileSync(join(numberingDir,'video.MP4'),'untouched video')
mkdirSync(join(numberingDir,'子目录'));writeFileSync(join(numberingDir,'子目录','nested.JPG'),'untouched image')
let modelCalls=0,notificationCalls=0,lastNotification=null
const model=createServer(async(req,res)=>{if(req.url==='/notification-status'){res.setHeader('content-type','application/json');res.end(JSON.stringify({count:notificationCalls,last:lastNotification}));return};if(req.method==='GET'&&req.url==='/v1/models'){res.setHeader('content-type','application/json');res.end(JSON.stringify({data:[{id:'视觉模型 · 本地测试'},{id:'second-model'}]}));return};let raw='';for await(const chunk of req){raw+=chunk};const body=JSON.parse(raw);if(req.url==='/notify'){notificationCalls++;lastNotification=body;res.setHeader('content-type','application/json');res.end('{}');return};if(body.messages?.length!==2||body.messages[0].role!=='system'||body.messages[1].role!=='user'||!body.messages[0].content.includes('用于测试的独立提示词')){res.statusCode=400;res.end('Unexpected prompt or shared conversation');return};modelCalls++;res.setHeader('content-type','application/json');res.end(JSON.stringify({choices:[{message:{content:'山间晨雾与日出'},finish_reason:'stop'}]}))})
await new Promise(resolve=>model.listen(0,'127.0.0.1',resolve))
const modelPort=model.address().port
mkdirSync(resolve('test-results'),{recursive:true})
writeFileSync(resolve('test-results/fixture.json'),JSON.stringify({modelURL:`http://127.0.0.1:${modelPort}/v1`}))
const cfg=join(base,'config.yaml')
writeFileSync(cfg,`listen: "127.0.0.1"\nport: 18790\nmedia-path: ${JSON.stringify(media)}\nstorage: ${JSON.stringify(join(base,'data'))}\ntmp-storage: ${JSON.stringify(join(base,'cache'))}\nauth:\n  username: "admin"\n  password: "test-password-123"\n`,{mode:0o600})
const child=spawn(resolve('../bin/video-rename'),['--config',cfg],{stdio:'inherit'})
let stopped=false
function stop(){if(stopped)return;stopped=true;child.kill('SIGTERM');model.close();setTimeout(()=>{rmSync(base,{recursive:true,force:true});process.exit(0)},1500)}
process.on('SIGTERM',stop);process.on('SIGINT',stop);child.on('exit',()=>{if(!stopped){model.close();rmSync(base,{recursive:true,force:true});process.exit(1)}})
