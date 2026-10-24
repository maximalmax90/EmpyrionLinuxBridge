package web

import (
	"bytes"
	"html/template"
	"testing"

	"elb/ui/internal/daemonclient"
)

const scenarioCacheFieldsetSnippet = `
{{$sc := .Config.ScenarioCache}}
{{$scStatus := .Health.ScenarioCache}}
{{$extIP := .Identity.ExternalIP}}
{{if eq $scStatus.Phase "packaging"}}
<p>{{t "settings.server.scenarioCache.statusPackaging"}}</p>
{{else if eq $scStatus.Phase "uploading"}}
<p>{{t "settings.server.scenarioCache.statusUploading"}}</p>
{{else if eq $scStatus.Phase "verifying"}}
<p>{{t "settings.server.scenarioCache.statusVerifying"}}</p>
{{else if eq $scStatus.Phase "failed"}}
<p>{{t "settings.server.scenarioCache.statusFailed"}}: {{$scStatus.Message}}</p>
{{end}}
<select id="scenarioCacheMode">
  <option value="off" {{if or (eq $sc.Mode "") (eq $sc.Mode "off")}}selected{{end}}>{{t "settings.server.scenarioCache.modeOff"}}</option>
  <option value="embedded" {{if eq $sc.Mode "embedded"}}selected{{end}}>{{t "settings.server.scenarioCache.modeEmbedded"}}</option>
  <option value="panel" {{if eq $sc.Mode "panel"}}selected{{end}}>{{t "settings.server.scenarioCache.modePanel"}}</option>
  <option value="own" {{if eq $sc.Mode "own"}}selected{{end}}>{{t "settings.server.scenarioCache.modeOwn"}}</option>
</select>
<div id="scenarioCacheEmbeddedFields" style="{{if ne $sc.Mode "embedded"}}display:none{{end}}">
  <input id="scenarioCacheEmbeddedListenAddr" value="{{$sc.EmbeddedListenAddr}}">
</div>
<div id="scenarioCachePanelFields" style="{{if ne $sc.Mode "panel"}}display:none{{end}}">
  <input id="scenarioCachePanelListenPort" value="{{if $sc.PanelListenPort}}{{$sc.PanelListenPort}}{{else}}{{.UIListenPort}}{{end}}">
</div>
{{$ftpUseKey := and (eq $sc.FTPProtocol "sftp") (ne $sc.FTPKey "")}}
<div id="scenarioCacheOwnFields" style="{{if ne $sc.Mode "own"}}display:none{{end}}">
  <select id="scenarioCacheFtpProtocol">
    <option value="ftp" {{if ne $sc.FTPProtocol "sftp"}}selected{{end}}>FTP</option>
    <option value="sftp" {{if eq $sc.FTPProtocol "sftp"}}selected{{end}}>SFTP</option>
  </select>
  <input id="scenarioCacheFtpHost" value="{{$sc.FTPHost}}">
  <input id="scenarioCacheFtpPort" value="{{if $sc.FTPPort}}{{$sc.FTPPort}}{{end}}">
  <input id="scenarioCacheFtpUser" value="{{$sc.FTPUser}}">
  <div id="scenarioCacheFtpAuthModeRow" style="{{if ne $sc.FTPProtocol "sftp"}}display:none{{end}}">
    <select id="scenarioCacheFtpAuthMode">
      <option value="password" {{if not $ftpUseKey}}selected{{end}}>{{t "settings.backup.ftpAuthPassword"}}</option>
      <option value="key" {{if $ftpUseKey}}selected{{end}}>{{t "settings.backup.ftpAuthKey"}}</option>
    </select>
  </div>
  <div id="scenarioCacheFtpPasswordRow" style="{{if $ftpUseKey}}display:none{{end}}">
    <input id="scenarioCacheFtpPassword" value="{{$sc.FTPPassword}}">
  </div>
  <input id="scenarioCacheFtpPath" value="{{$sc.FTPPath}}">
</div>
<div id="scenarioCacheFtpKeyFields" style="{{if or (ne $sc.Mode "own") (not $ftpUseKey)}}display:none{{end}}">
  <textarea id="scenarioCacheFtpKey">{{$sc.FTPKey}}</textarea>
</div>
<div id="scenarioCachePublicUrlFields" style="{{if or (eq $sc.Mode "") (eq $sc.Mode "off")}}display:none{{end}}">
  <input id="scenarioCachePublicUrlBase" value="{{$sc.PublicURLBase}}" data-ext-ip="{{$extIP}}">
</div>
<button id="scenarioCacheTestBtn" style="{{if ne $sc.Mode "own"}}display:none{{end}}">{{t "settings.server.scenarioCache.test"}}</button>
`

func TestScenarioCacheFieldset_Executes(t *testing.T) {
	tmpl, err := template.New("snippet").Funcs(funcMap).Parse(scenarioCacheFieldsetSnippet)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	cases := []struct {
		name     string
		config   daemonclient.ScenarioCacheSettings
		status   daemonclient.ScenarioCacheStatus
		identity daemonclient.Identity
	}{
		{"zero value (fresh install)", daemonclient.ScenarioCacheSettings{}, daemonclient.ScenarioCacheStatus{}, daemonclient.Identity{}},
		{"off with a stale PublicURLBase from a previous mode", daemonclient.ScenarioCacheSettings{Mode: "off", PublicURLBase: "http://stale.example:8899"}, daemonclient.ScenarioCacheStatus{}, daemonclient.Identity{}},
		{"embedded mode, packaging, no ext ip yet", daemonclient.ScenarioCacheSettings{Mode: "embedded", PublicURLBase: "http://host:8899", EmbeddedListenAddr: ":8899"}, daemonclient.ScenarioCacheStatus{Phase: "packaging"}, daemonclient.Identity{}},
		{"embedded mode with ext ip", daemonclient.ScenarioCacheSettings{Mode: "embedded", PublicURLBase: "http://1.2.3.4:8899", EmbeddedListenAddr: ":8899"}, daemonclient.ScenarioCacheStatus{Phase: "done"}, daemonclient.Identity{ExternalIP: "1.2.3.4"}},
		{"panel mode, uploading", daemonclient.ScenarioCacheSettings{Mode: "panel", PublicURLBase: "http://elb.home:8080/scenario-cache"}, daemonclient.ScenarioCacheStatus{Phase: "uploading"}, daemonclient.Identity{}},
		{"panel mode with separate port", daemonclient.ScenarioCacheSettings{Mode: "panel", PublicURLBase: "http://elb.home:8899", PanelListenPort: 8899}, daemonclient.ScenarioCacheStatus{Phase: "done"}, daemonclient.Identity{}},
		{"own mode, plain ftp with password", daemonclient.ScenarioCacheSettings{Mode: "own", PublicURLBase: "https://files.example.com", FTPProtocol: "ftp", FTPHost: "ftp.example.com", FTPPort: 21, FTPUser: "u", FTPPassword: "p", FTPPath: "/"}, daemonclient.ScenarioCacheStatus{}, daemonclient.Identity{}},
		{"own mode, sftp with password", daemonclient.ScenarioCacheSettings{Mode: "own", PublicURLBase: "https://files.example.com", FTPProtocol: "sftp", FTPHost: "ftp.example.com", FTPPort: 22, FTPUser: "u", FTPPassword: "p", FTPPath: "/"}, daemonclient.ScenarioCacheStatus{}, daemonclient.Identity{}},
		{"own mode, sftp with key, verifying", daemonclient.ScenarioCacheSettings{Mode: "own", PublicURLBase: "https://files.example.com", FTPProtocol: "sftp", FTPHost: "ftp.example.com", FTPPort: 22, FTPUser: "u", FTPKey: "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----", FTPPath: "/"}, daemonclient.ScenarioCacheStatus{Phase: "verifying"}, daemonclient.Identity{}},
		{"failed, fallback message", daemonclient.ScenarioCacheSettings{Mode: "own", PublicURLBase: "https://files.example.com"}, daemonclient.ScenarioCacheStatus{Phase: "failed", Message: "dial tcp: connection refused"}, daemonclient.Identity{}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := map[string]any{
				"Config":       daemonclient.AppConfig{ScenarioCache: c.config},
				"Health":       daemonclient.Health{Status: daemonclient.Status{ScenarioCache: c.status}},
				"Identity":     c.identity,
				"UIListenPort": 8080,
			}
			var buf bytes.Buffer
			if err := tmpl.Execute(&buf, data); err != nil {
				t.Fatalf("execute: %v", err)
			}
		})
	}
}
