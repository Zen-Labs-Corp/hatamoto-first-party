# Policy files

Both files are embedded into the plugin (`names.go`). They are read at build
time only; the plugin never fetches anything at runtime. Lines starting with `#`
and blank lines are comments. Every other line is one lowercase package name.

## reserved-names.txt

Names no developer may submit. Copied byte for byte from the public repository
`Zen-Labs-Corp/hatamoto-plugins`, file `policy/reserved-names.txt`, commit
`ba78cc1`, sha256
`a8e934212476d4603a706d381c9d19e0e6ff66a4b1598f586520c20a50445120`.

    sha256sum policy/reserved-names.txt

## first-party-names.txt

Names that already exist as first-party or community packages (plugins and
themes), plus `searchconsole`, which core treats as first-party. Regenerate it
when a package is added:

    {
      ls <first-party checkout>/plugins
      ls <hatamoto-plugins checkout>/manifests | sed -n 's/\.json$//p'
      ls <hatamoto-plugins checkout>/manifests/themes | sed 's/\.json$//'
      curl -s https://hatamoto.dev/registry.json | jq -r '.index.packages[].name'
      echo searchconsole
    } | grep -E '^[a-z][a-z0-9-]{1,39}$' | sort -u

then put the two comment lines back at the top.
