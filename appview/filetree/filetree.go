package filetree

import (
	"path/filepath"
	"sort"
	"strings"
)

type FileTreeNode struct {
	Name        string
	Path        string
	IsDirectory bool
	Children    map[string]*FileTreeNode
}

// NewNode creates a new node
func newNode(name, path string, isDir bool) *FileTreeNode {
	return &FileTreeNode{
		Name:        name,
		Path:        path,
		IsDirectory: isDir,
		Children:    make(map[string]*FileTreeNode),
	}
}

func FileTree(files []string) *FileTreeNode {
	rootNode := newNode("", "", true)

	sort.Strings(files)

	for _, file := range files {
		if file == "" {
			continue
		}

		parts := strings.Split(filepath.Clean(file), "/")
		if len(parts) == 0 {
			continue
		}

		currentNode := rootNode
		currentPath := ""

		for i, part := range parts {
			if currentPath == "" {
				currentPath = part
			} else {
				currentPath = filepath.Join(currentPath, part)
			}

			isDir := i < len(parts)-1

			if _, exists := currentNode.Children[part]; !exists {
				currentNode.Children[part] = newNode(part, currentPath, isDir)
			}

			currentNode = currentNode.Children[part]
		}
	}

	return rootNode
}
