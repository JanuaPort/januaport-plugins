package hygiene

import (
 "fmt"
 "os"
 "reflect"
 "strings"
 "testing"

 "gopkg.in/yaml.v3"
)

type compose struct {
 Name string `yaml:"name"`
 Services map[string]service `yaml:"services"`
 Networks map[string]network `yaml:"networks"`
}
type network struct { External bool `yaml:"external"`; Name string `yaml:"name"` }
type service struct {
 Image string `yaml:"image"`
 ReadOnly bool `yaml:"read_only"`
 User string `yaml:"user"`
 CapDrop []string `yaml:"cap_drop"`
 SecurityOpt []string `yaml:"security_opt"`
 Environment map[string]string `yaml:"environment"`
 Volumes []string `yaml:"volumes"`
 Networks []string `yaml:"networks"`
 Restart string `yaml:"restart"`
 MemLimit string `yaml:"mem_limit"`
 PidsLimit int `yaml:"pids_limit"`
 Healthcheck struct {Test []string `yaml:"test"`; Interval string `yaml:"interval"`;Timeout string `yaml:"timeout"`;Retries int `yaml:"retries"`} `yaml:"healthcheck"`
}
func checkCompose(b string) error {
 var c compose;d:=yaml.NewDecoder(strings.NewReader(b));d.KnownFields(true);if e:=d.Decode(&c);e!=nil{return e}
 s,ok:=c.Services["agent-runner"];if !ok||len(c.Services)!=1{return fmt.Errorf("services")}
 if !reflect.DeepEqual(c.Networks,map[string]network{"plugins":{true,"jnpt_plugins"}})||!reflect.DeepEqual(s.Networks,[]string{"plugins"}){return fmt.Errorf("network")}
 if !s.ReadOnly||s.User!="65532:65532"||!reflect.DeepEqual(s.CapDrop,[]string{"ALL"})||!reflect.DeepEqual(s.SecurityOpt,[]string{"no-new-privileges:true"}){return fmt.Errorf("hardening")}
 if !reflect.DeepEqual(s.Volumes,[]string{"/run/jnpt/plugins/agent-runner:/run/jnpt/plugins/agent-runner:ro"}){return fmt.Errorf("mount")}
 if !reflect.DeepEqual(s.Environment,map[string]string{"JNPT_GATEWAY_URL":"${JNPT_GATEWAY_URL:-http://jnpt:8484}"}){return fmt.Errorf("environment")}
 if s.MemLimit!="256m"||s.PidsLimit!=64||!reflect.DeepEqual(s.Healthcheck.Test,[]string{"CMD","/agent-runner","healthcheck"}){return fmt.Errorf("resources/health")}
 return nil
}
func read(t *testing.T,p string) string {t.Helper();b,e:=os.ReadFile("../../"+p);if e!=nil{t.Fatal(e)};return string(b)}
func TestCompose(t *testing.T) {if e:=checkCompose(read(t,"docker-compose.yml"));e!=nil{t.Fatal(e)}}
func TestComposeForbiddenPaths(t *testing.T) {
 original:=read(t,"docker-compose.yml")
 for _,key:=range []string{"ports: [\"8090:8090\"]","network_mode: host","privileged: true","extra_hosts: [\"host:host-gateway\"]","devices: [/dev/net/tun]","volumes_from: [other]","pid: host","ipc: host","userns_mode: host","uts: host","sysctls: {net.ipv4.ip_forward: 1}","build: .","env_file: .env"}{t.Run(key,func(t *testing.T){if e:=checkCompose(strings.Replace(original,"    read_only: true","    "+key+"\n    read_only: true",1));e==nil{t.Fatal("forbidden key accepted")}})}
 for _,pair:=range [][2]string{{"networks: [plugins]","networks: [plugins, external]"},{"    name: jnpt_plugins","    name: jnpt_default"},{"    external: true","    external: false"},{"read_only: true","read_only: false"},{"65532:65532","0:0"},{"cap_drop: [ALL]","cap_drop: []"},{"no-new-privileges:true","no-new-privileges:false"},{"agent-runner:ro","agent-runner:rw"},{"JNPT_GATEWAY_URL: ${JNPT_GATEWAY_URL:-http://jnpt:8484}","ANTHROPIC_API_KEY: secret"}} {if e:=checkCompose(strings.Replace(original,pair[0],pair[1],1));e==nil{t.Fatalf("mutation survived: %v",pair)}}
}
func checkDocker(s string) error {
 for _,want:=range []string{"CGO_ENABLED=0 GOOS=linux go build","FROM gcr.io/distroless/static:nonroot@sha256:","USER 65532:65532",`ENTRYPOINT ["/agent-runner"]`}{if !strings.Contains(s,want){return fmt.Errorf("missing %s",want)}}
 return nil
}
func TestDockerNonRootStatic(t *testing.T) {s:=read(t,"Dockerfile");if e:=checkDocker(s);e!=nil{t.Fatal(e)};for _,key:=range []string{"CGO_ENABLED=0","USER 65532:65532","distroless/static:nonroot"}{if checkDocker(strings.ReplaceAll(s,key,"REMOVED"))==nil{t.Fatal(key)}}}
