# No Windows artifact or native adapter profile is qualified for this preview.
# Fail before downloading or changing configuration; never bypass verification.
$ErrorActionPreference = 'Stop'
throw 'PeerTurn 0.1.1-preview supports Linux/macOS only. Use an environment in the documented support matrix and the reviewed scripts/install.sh (SHA-256 plus Sigstore verification required).'
