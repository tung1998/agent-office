// Bản đồ văn phòng: a small server for the company's office map. It reads
// office's database (read only, see office-db.mjs) for this repo's project
// and the projects it links to. Node 22.5+ (or the sqlite3 command), no
// packages: `node office-map/server.mjs`, then open http://127.0.0.1:4710
// (PORT changes it; office's watch widget shows it too).
import { createServer } from 'node:http'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { openOffice } from './office-db.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const port = Number(process.env.PORT) || 4710
const host = process.env.HOST || '127.0.0.1'
const files = { '/': ['index.html', 'text/html'], '/map.js': ['map.js', 'text/javascript'] }

let office // opened on the first request: office may not know the project yet when this starts
async function data(res) {
  try {
    office ??= await openOffice(dirname(here))
    res.writeHead(200, { 'content-type': 'application/json', 'cache-control': 'no-store' })
    res.end(JSON.stringify(office.load(120)))
  } catch (e) {
    office = undefined
    res.writeHead(503, { 'content-type': 'application/json' })
    res.end(JSON.stringify({ error: e.message }))
  }
}

createServer(async (req, res) => {
  const path = new URL(req.url, 'http://x').pathname
  if (path === '/data') return data(res)
  const f = files[path]
  if (!f) {
    res.writeHead(404)
    return res.end()
  }
  res.writeHead(200, { 'content-type': `${f[1]}; charset=utf-8`, 'cache-control': 'no-store' })
  res.end(await readFile(join(here, f[0])))
}).listen(port, host, () => {
  console.log(`Bản đồ văn phòng: http://${host}:${port}`)
})
