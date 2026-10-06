// The machine's figures, pushed by the server (ADR-078): the header's summary
// comes to every admin page by itself; the whole picture (processes) while a
// page shows the Máy tab, which turns the "machine" topic on for its stream.
// Nothing here asks every few seconds.
export interface SysSummary { cpu_percent: number, mem_used: number, mem_total: number, procs: number, agents: number, tasks?: string[] }
export interface SysProc { pid: number, ppid: number, name: string, cmd: string, cpu: number, mem: number, started_at: string, depth: number }
export interface SysGroup {
  kind: 'agent' | 'automation' | 'project' | 'other', label: string, sub?: string, pid: number, turn_id?: string, conversation_id?: string,
  project_id?: string, process_id?: string, cpu: number, mem: number, started_at: string, procs: SysProc[]
}
export interface SysStats {
  machine: { cpu_percent: number, cores: number[], mem_total: number, mem_used: number, disk_path: string, disk_total: number, disk_used: number, load?: number[], uptime_s: number, net_rx: number, net_tx: number, host: string, os: string }
  office: SysProc & { goroutines: number }
  groups: SysGroup[]
  summary: SysSummary
}

export const sysSummary = ref<SysSummary | null>(null)
export const sysStats = ref<SysStats | null>(null)
let machineWanted = 0

// the stream's events (useDataSync's startLive)
export function onSysEvent(name: string, data: unknown) {
  if (name === 'stats') sysSummary.value = data as SysSummary
  if (name === 'machine') {
    sysStats.value = data as SysStats
    sysSummary.value = sysStats.value.summary
  }
}
// a new stream (first, or after a reconnect): the topics again
export function onSysStream() {
  if (machineWanted > 0) void liveTopic('machine', true)
}

export function useSystemStats(level: 'summary' | 'full') {
  let on = false
  const want = () => {
    if (on || level !== 'full') return
    on = true
    if (machineWanted++ === 0) void liveTopic('machine', true)
  }
  const drop = () => {
    if (!on) return
    on = false
    if (--machineWanted === 0) void liveTopic('machine', false)
  }
  onMounted(want)
  onActivated(want)
  onDeactivated(drop)
  onBeforeUnmount(drop)
  return { summary: sysSummary, stats: sysStats }
}
