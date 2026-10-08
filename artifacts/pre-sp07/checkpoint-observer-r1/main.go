package main
import("os";"fmt";"encoding/json";"crypto/sha256";"ops-platform/internal/profile")
func digest(v any)string{b,_:=json.Marshal(v);return fmt.Sprintf("sha256:%x",sha256.Sum256(b))}
func main(){p,e:=profile.ReadResolvedProfileFile(os.Args[1]);if e!=nil{panic(e)};raw,e:=os.ReadFile(os.Args[2]);if e!=nil{panic(e)};var b map[string]any;if json.Unmarshal(raw,&b)!=nil{panic("invalid public business input")};for _,s:=range b["sp04"].(map[string]any)["sources"].([]any){q:=s.(map[string]any)["ScopeProbe"].(map[string]any);delete(q,"from");delete(q,"to")};json.NewEncoder(os.Stdout).Encode(map[string]string{"profileDigest":digest(p),"businessDigest":digest(b)})}
