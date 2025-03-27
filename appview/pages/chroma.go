package pages

import "github.com/alecthomas/chroma/v2"

var tangledTheme map[chroma.TokenType]string = map[chroma.TokenType]string{
	// Keywords
	chroma.Keyword:            "text-blue-400",
	chroma.KeywordConstant:    "text-indigo-400",
	chroma.KeywordDeclaration: "text-purple-400",
	chroma.KeywordNamespace:   "text-teal-400",
	chroma.KeywordReserved:    "text-pink-400",

	// Names
	chroma.Name:          "text-gray-700",
	chroma.NameFunction:  "text-green-500",
	chroma.NameClass:     "text-orange-400",
	chroma.NameNamespace: "text-cyan-500",
	chroma.NameVariable:  "text-red-400",
	chroma.NameBuiltin:   "text-yellow-500",

	// Literals
	chroma.LiteralString:      "text-emerald-500 ",
	chroma.LiteralStringChar:  "text-lime-500",
	chroma.LiteralNumber:      "text-rose-400",
	chroma.LiteralNumberFloat: "text-amber-500",

	// Operators
	chroma.Operator:     "text-blue-500",
	chroma.OperatorWord: "text-indigo-500",

	// Comments
	chroma.Comment:       "text-gray-500 italic",
	chroma.CommentSingle: "text-gray-400 italic",

	// Generic
	chroma.GenericError:    "text-red-600",
	chroma.GenericHeading:  "text-purple-500 font-bold",
	chroma.GenericDeleted:  "text-red-400 line-through",
	chroma.GenericInserted: "text-green-400 underline",
}
