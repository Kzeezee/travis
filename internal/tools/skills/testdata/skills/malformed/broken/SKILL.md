---
name: broken
description: never gets read, there is no closing delimiter

# This file is deliberately missing the closing "---", so frontmatter parsing fails
# and discover.go must skip it with a diagnostic, never fatally.
