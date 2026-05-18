package main

import (
  "fmt"
  "os"
  "path/filepath"
  "go-ut-bench/internal/dataset"
)

func main(){
 projects := []struct{lang, name string}{
  {"java","commons-text"},{"java","jsoup"},{"java","gson"},{"cpp","CLI11"},{"go","go-playground-validator"},{"java","java-diff-utils"},{"cpp","magic_enum"},{"go","go-playground-form"},{"python","humanize"},{"go","fatih-color"},{"cpp","fmt"},
 }
 for _, p := range projects {
  root := filepath.Join("..","datasets",p.lang,p.lang+"_code_files_repo_level","oss",p.name)
  count := 0
  _ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
    if err != nil || d.IsDir() { return nil }
    if _, ok := dataset.SynthesizeRepoLevelMeta(path); ok { count++ }
    return nil
  })
  fmt.Printf("%s/%s modules=%d\n", p.lang,p.name,count)
 }
}
