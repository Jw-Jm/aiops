package main
import("context";"os";"ops-platform/internal/profile")
func main(){ctx:=context.Background();p,e:=profile.ReadProfileFile("deploy/profiles/dev-orbstack.yaml");if e!=nil{panic(e)};for _,n:=range []string{"postgresql","keycloak","seaweedfs","openbao","victoriaMetrics","victoriaLogs","vmalert"}{c:=p.Components[n];c.Mode="detect";c.DetectExisting=true;p.Components[n]=c};d,e:=profile.Discover(ctx,"orbstack","","bundle/component-catalog.yaml");if e!=nil{panic(e)};out,e:=profile.Detect(p,d);if e!=nil{panic(e)};if e=profile.WriteYAML(os.Args[1],out);e!=nil{panic(e)}}
