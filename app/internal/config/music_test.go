package config
import "testing"
func TestDefaultMusicConfigIsValid(t *testing.T){c:=DefaultMusicConfig();if e:=c.Validate();e!=nil{t.Fatal(e)};if !c.Enabled||!c.CommandEnabled||c.QueueCapacity!=100{t.Fatalf("defaults: %+v",c)}}
func TestMusicConfigMigrationDropsPlatformConfig(t *testing.T){c,changed:=MigrateMusicConfig(MusicConfig{ConfigVersion:3,Enabled:true,CommandEnabled:true,CommandPrefix:"点歌",QueueCapacity:80});if !changed{t.Fatal("expected migration")};if e:=c.Validate();e!=nil{t.Fatal(e)};if c.ConfigVersion!=4||c.QueueCapacity!=80{t.Fatalf("migrated: %+v",c)}}
