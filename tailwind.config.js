/** @type {import('tailwindcss').Config} */
const colors = require("tailwindcss/colors");

module.exports = {
	content: ["./appview/pages/templates/**/*.html", "./appview/pages/chroma.go"],
	darkMode: "media",
	theme: {
		container: {
			padding: "2rem",
			center: true,
			screens: {
				sm: "500px",
				md: "600px",
				lg: "800px",
				xl: "1000px",
				"2xl": "1200px",
			},
		},
		extend: {
			fontFamily: {
				sans: ["InterVariable", "system-ui", "sans-serif", "ui-sans-serif"],
				mono: [
					"IBMPlexMono",
					"ui-monospace",
					"SFMono-Regular",
					"Menlo",
					"Monaco",
					"Consolas",
					"Liberation Mono",
					"Courier New",
					"monospace",
				],
			},
			typography: {
				DEFAULT: {
					css: {
						maxWidth: "none",
						pre: {
							"@apply font-normal text-black bg-gray-100 dark:bg-gray-900 dark:text-gray-300 dark:border-gray-700 border": {},
						},
						code: {
							"@apply font-normal font-mono p-1 rounded text-black bg-gray-100 dark:bg-gray-900 dark:text-gray-300 dark:border-gray-700": {},
						},
						"code::before": {
							content: '""',
						},
						"code::after": {
							content: '""',
						},
						blockquote: {
							quotes: "none",
						},
						'h1, h2, h3, h4': {
							"@apply mt-4 mb-2": {}
						},
						h1: {
							"@apply mt-3 pb-3 border-b border-gray-300 dark:border-gray-600": {}
						},
						h2: {
							"@apply mt-3 pb-3 border-b border-gray-200 dark:border-gray-700": {}
						},
						h3: {
							"@apply mt-2": {}
						},
					},
				},
			},
			gridTemplateColumns: {
				'14': 'repeat(14, minmax(0, 1fr))',
				'28': 'repeat(28, minmax(0, 1fr))',
			}
		},
	},
	plugins: [require("@tailwindcss/typography")],
};
