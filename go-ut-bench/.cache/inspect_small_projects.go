package main

import (
  "fmt"
  "os"
  "path/filepath"
  "sort"
  "strings"
  "go-ut-bench/internal/dataset"
)

type proj struct{ lang, name string }
func main(){
 projects := []proj{{"cpp","magic_enum"},{"go","go-playground-form"},{"python","humanize"},{"go","fatih-color"}}
 for _, p := range projects {
  root := filepath.Join("..","datasets",p.lang,p.lang+"_code_files_repo_level","oss",p.name)
  var accepted, rejected []string
  _ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
    if err != nil || d.IsDir(){ return nil }
    rel, _ := filepath.Rel(root,path); rel = filepath.ToSlash(rel)
    ext := strings.ToLower(filepath.Ext(path))
    if ext != ".go" && ext != ".py" && ext != ".cpp" && ext != ".cc" && ext != ".cxx" && ext != ".c++" && ext != ".java" { return nil }
    if meta, ok := dataset.SynthesizeRepoLevelMeta(path); ok { accepted = append(accepted, meta.TargetFile) } else { rejected = append(rejected, rel) }
    return nil
  })
  sort.Strings(accepted); sort.Strings(rejected)
  fmt.Printf("\n%s/%s accepted=%d rejected_src_like=%d\n", p.lang,p.name,len(accepted),len(rejected))
  fmt.Println("accepted:")
  for _, x := range accepted { fmt.Println("  "+x) }
  if len(rejected)>0 { fmt.Println("rejected:"); for _, x := range rejected { fmt.Println("  "+x) } }
 }
}
