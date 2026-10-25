# Decodes the root token from a generate-root attempt (docs/openbao.md, "Recovery key").
# pki/openbao/decode-root-token.sh <encoded_token> <otp>     (needs perl)
#
# `bao operator generate-root -decode` cannot be used without a token: it first
# reads the attempt's status from an authenticated endpoint, even on the loopback
# listener. The encoding (sdk/helper/roottoken in OpenBao) is raw base64 of the
# token XORed with the OTP.
set -euo pipefail
perl -MMIME::Base64 -e 'print decode_base64($ARGV[0]) ^ $ARGV[1]' "$1" "$2"
