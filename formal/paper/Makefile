# Builds the paper into its single committed home, docs/static/papers/.
#
#   make -C formal/paper        # build the PDF
#   make -C formal/paper check  # build and fail on overfull boxes or undefined references
#
# Requires tectonic (https://tectonic-typesetting.github.io). The first build
# downloads the TeX packages it needs. tectonic writes no intermediate files
# next to the sources.

REPO_ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST)))/../..)
PAPER_SOURCE := $(REPO_ROOT)/formal/paper/dex-expressiveness.tex
PAPER_SOURCES := $(PAPER_SOURCE) $(wildcard $(REPO_ROOT)/formal/paper/sections/*.tex) \
	$(REPO_ROOT)/formal/paper/references.bib
PAPER_OUTPUT_DIR := $(REPO_ROOT)/docs/static/papers
PAPER_PDF := $(PAPER_OUTPUT_DIR)/dex-expressiveness.pdf

.PHONY: all check

all: $(PAPER_PDF)

$(PAPER_PDF): $(PAPER_SOURCES)
	mkdir -p $(PAPER_OUTPUT_DIR)
	tectonic -o $(PAPER_OUTPUT_DIR) $(PAPER_SOURCE)

check:
	mkdir -p $(PAPER_OUTPUT_DIR)
	tectonic --print -o $(PAPER_OUTPUT_DIR) $(PAPER_SOURCE) 2>&1 | \
		awk '/Rerunning TeX/ { report = ""; found = 0 } \
			/Overfull \\hbox|undefined|multiply defined/ { report = report $$0 "\n"; found = 1 } \
			END { printf "%s", report; exit found }'
