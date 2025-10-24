SHELL := /bin/bash

.PHONY: check-i18n
check-i18n:
	@echo "Running i18n checks (warning-only)…"
	@bash scripts/check-i18n.sh
	@echo "Done."

