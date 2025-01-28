package tmpl

import (
	"html/template"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func Load(tpath string) (*template.Template, error) {
	tmpl := template.New("")
	loadedTemplates := make(map[string]bool)

	err := filepath.Walk(tpath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() && strings.HasSuffix(path, ".html") {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			relPath, err := filepath.Rel(tpath, path)
			if err != nil {
				return err
			}

			name := strings.TrimSuffix(relPath, ".html")
			name = strings.ReplaceAll(name, string(filepath.Separator), "/")

			_, err = tmpl.New(name).Parse(string(content))
			if err != nil {
				log.Printf("error parsing template %s: %v", name, err)
				return err
			}

			loadedTemplates[name] = true
			log.Printf("loaded template: %s", name)
			return err
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	log.Printf("total templates loaded: %d", len(loadedTemplates))
	return tmpl, nil

}
