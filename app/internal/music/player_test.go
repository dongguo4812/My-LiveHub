package music
import "testing"
type testMetadataSource struct{id,name string}
func(s testMetadataSource)ID()string{return s.id}
func(s testMetadataSource)Name()string{return s.name}
func(s testMetadataSource)Capabilities()SourceCapabilities{return SourceCapabilities{}}
func TestSourceRegistryRegisterAndList(t *testing.T){r:=NewSourceRegistry();if e:=r.Register(testMetadataSource{"lx_custom","洛雪自定义音源"});e!=nil{t.Fatal(e)};got:=r.List(map[string]bool{"lx_custom":true});if len(got)!=1||got[0].ID!="lx_custom"||!got[0].Enabled{t.Fatalf("got %#v",got)}}
