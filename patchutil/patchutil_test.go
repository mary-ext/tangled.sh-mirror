package patchutil

import (
	"reflect"
	"testing"
)

func TestSplitPatches(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "Empty input",
			input:    "",
			expected: []string{},
		},
		{
			name:     "No valid patches",
			input:    "This is not a patch\nJust some random text",
			expected: []string{},
		},
		{
			name: "Single patch",
			input: `From 3c5035488318164b81f60fe3adcd6c9199d76331 Mon Sep 17 00:00:00 2001
From: Author <author@example.com>
Date: Wed, 16 Apr 2025 11:01:00 +0300
Subject: [PATCH] Example patch

diff --git a/file.txt b/file.txt
index 123456..789012 100644
--- a/file.txt
+++ b/file.txt
@@ -1 +1 @@
-old content
+new content
--
2.48.1`,
			expected: []string{
				`From 3c5035488318164b81f60fe3adcd6c9199d76331 Mon Sep 17 00:00:00 2001
From: Author <author@example.com>
Date: Wed, 16 Apr 2025 11:01:00 +0300
Subject: [PATCH] Example patch

diff --git a/file.txt b/file.txt
index 123456..789012 100644
--- a/file.txt
+++ b/file.txt
@@ -1 +1 @@
-old content
+new content
--
2.48.1`,
			},
		},
		{
			name: "Two patches",
			input: `From 3c5035488318164b81f60fe3adcd6c9199d76331 Mon Sep 17 00:00:00 2001
From: Author <author@example.com>
Date: Wed, 16 Apr 2025 11:01:00 +0300
Subject: [PATCH 1/2] First patch

diff --git a/file1.txt b/file1.txt
index 123456..789012 100644
--- a/file1.txt
+++ b/file1.txt
@@ -1 +1 @@
-old content
+new content
--
2.48.1
From a9529f3b3a653329a5268f0f4067225480207e3c Mon Sep 17 00:00:00 2001
From: Author <author@example.com>
Date: Wed, 16 Apr 2025 11:03:11 +0300
Subject: [PATCH 2/2] Second patch

diff --git a/file2.txt b/file2.txt
index abcdef..ghijkl 100644
--- a/file2.txt
+++ b/file2.txt
@@ -1 +1 @@
-foo bar
+baz qux
--
2.48.1`,
			expected: []string{
				`From 3c5035488318164b81f60fe3adcd6c9199d76331 Mon Sep 17 00:00:00 2001
From: Author <author@example.com>
Date: Wed, 16 Apr 2025 11:01:00 +0300
Subject: [PATCH 1/2] First patch

diff --git a/file1.txt b/file1.txt
index 123456..789012 100644
--- a/file1.txt
+++ b/file1.txt
@@ -1 +1 @@
-old content
+new content
--
2.48.1`,
				`From a9529f3b3a653329a5268f0f4067225480207e3c Mon Sep 17 00:00:00 2001
From: Author <author@example.com>
Date: Wed, 16 Apr 2025 11:03:11 +0300
Subject: [PATCH 2/2] Second patch

diff --git a/file2.txt b/file2.txt
index abcdef..ghijkl 100644
--- a/file2.txt
+++ b/file2.txt
@@ -1 +1 @@
-foo bar
+baz qux
--
2.48.1`,
			},
		},
		{
			name: "Patches with additional text between them",
			input: `Some text before the patches

From 3c5035488318164b81f60fe3adcd6c9199d76331 Mon Sep 17 00:00:00 2001
From: Author <author@example.com>
Subject: [PATCH] First patch

diff content here
--
2.48.1

Some text between patches

From a9529f3b3a653329a5268f0f4067225480207e3c Mon Sep 17 00:00:00 2001
From: Author <author@example.com>
Subject: [PATCH] Second patch

more diff content
--
2.48.1

Text after patches`,
			expected: []string{
				`From 3c5035488318164b81f60fe3adcd6c9199d76331 Mon Sep 17 00:00:00 2001
From: Author <author@example.com>
Subject: [PATCH] First patch

diff content here
--
2.48.1

Some text between patches`,
				`From a9529f3b3a653329a5268f0f4067225480207e3c Mon Sep 17 00:00:00 2001
From: Author <author@example.com>
Subject: [PATCH] Second patch

more diff content
--
2.48.1

Text after patches`,
			},
		},
		{
			name: "Patches with whitespace padding",
			input: `

From 3c5035488318164b81f60fe3adcd6c9199d76331 Mon Sep 17 00:00:00 2001
From: Author <author@example.com>
Subject: Patch

content
--
2.48.1


From a9529f3b3a653329a5268f0f4067225480207e3c Mon Sep 17 00:00:00 2001
From: Author <author@example.com>
Subject: Another patch

content
--
2.48.1
  `,
			expected: []string{
				`From 3c5035488318164b81f60fe3adcd6c9199d76331 Mon Sep 17 00:00:00 2001
From: Author <author@example.com>
Subject: Patch

content
--
2.48.1`,
				`From a9529f3b3a653329a5268f0f4067225480207e3c Mon Sep 17 00:00:00 2001
From: Author <author@example.com>
Subject: Another patch

content
--
2.48.1`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := splitFormatPatch(tt.input)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("splitPatches() = %v, want %v", result, tt.expected)
			}
		})
	}
}
