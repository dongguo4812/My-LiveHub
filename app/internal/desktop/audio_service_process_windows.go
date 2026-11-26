//go:build windows

package desktop

import (
 "context"
 "encoding/json"
 "fmt"
 "os/exec"
 "path/filepath"
 "sort"
 "strings"
 "time"
)

type webView2Process struct {
 PID int `json:"ProcessId"`
 ParentPID int `json:"ParentProcessId"`
 CommandLine string `json:"CommandLine"`
 StartedAt string `json:"StartedAt"`
 ExecutablePath string `json:"ExecutablePath"`
}

func readOwnedWebView2AudioServices(ownerPID int, now time.Time) ([]AudioServiceProcessSnapshot, error) {
 ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
 defer cancel()
 script := `$ErrorActionPreference='Stop'; Get-CimInstance Win32_Process -Filter "Name='msedgewebview2.exe'" | ForEach-Object { [PSCustomObject]@{ ProcessId=$_.ProcessId; ParentProcessId=$_.ParentProcessId; CommandLine=$_.CommandLine; StartedAt=if ($_.CreationDate) {$_.CreationDate.ToString('o')} else {''}; ExecutablePath=$_.ExecutablePath } } | ConvertTo-Json -Compress`
 output, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script).Output()
 if err != nil { return nil, fmt.Errorf("enumerate WebView2 processes with CIM: %w", err) }
 raw := strings.TrimSpace(string(output))
 if raw == "" || raw == "null" { return []AudioServiceProcessSnapshot{}, nil }
 var processes []webView2Process
 if err := json.Unmarshal([]byte(raw), &processes); err != nil {
  var one webView2Process
  if e := json.Unmarshal([]byte(raw), &one); e != nil { return nil, fmt.Errorf("decode WebView2 process inventory: %w", err) }
  processes = []webView2Process{one}
 }
 byPID := make(map[int]webView2Process, len(processes))
 for _, p := range processes { byPID[p.PID] = p }
 owned := func(pid int) bool {
  seen := map[int]bool{}
  for pid > 0 {
   if pid == ownerPID { return true }
   if seen[pid] { return false }; seen[pid] = true
   p, ok := byPID[pid]; if !ok { return false }; pid = p.ParentPID
  }
  return false
 }
 result := []AudioServiceProcessSnapshot{}
 for _, p := range processes {
  command := strings.ToLower(p.CommandLine)
  if !strings.Contains(command, "--type=utility") || !strings.Contains(command, "--utility-sub-type=audio.mojom.audioservice") || !owned(p.PID) { continue }
  started, _ := time.Parse(time.RFC3339Nano, p.StartedAt)
  dataDir := ""
  if i := strings.Index(command, "--user-data-dir="); i >= 0 {
   value := strings.TrimSpace(p.CommandLine[i+len("--user-data-dir="):])
   if len(value)>0 && (value[0]==34 || value[0]==39) { q:=value[0]; value=value[1:]; if end:=strings.IndexByte(value,q); end>=0 { value=value[:end] } } else if end:=strings.IndexAny(value," \t"); end>=0 { value=value[:end] }
   dataDir=filepath.Clean(value)
  }
  uptime:=int64(0); if !started.IsZero() && now.After(started) { uptime=int64(now.Sub(started).Seconds()) }
  result=append(result,AudioServiceProcessSnapshot{PID:p.PID,ParentPID:p.ParentPID,UserDataDir:dataDir,StartedAt:started,UptimeSeconds:uptime,ExecutablePath:p.ExecutablePath,CommandLine:p.CommandLine})
 }
 sort.Slice(result,func(i,j int)bool{return result[i].PID<result[j].PID})
 return result,nil
}
