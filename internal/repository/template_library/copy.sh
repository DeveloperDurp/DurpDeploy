set -euo pipefail

: "${INSTALL_PATH:?Set INSTALL_PATH}"
source_file=${FILE_SOURCE:-"${DURPDEPLOY_STAGE_DIR:?Managed staging is required}/artifact"}

if [[ "$source_file" != /* || "$INSTALL_PATH" != /* || "$INSTALL_PATH" == / ]]; then
    echo 'Use absolute file paths' >&2
    exit 1
fi

if [[ ! -f "$source_file" ]]; then
    echo 'Source file does not exist' >&2
    exit 1
fi

install -T -m 0755 -- "$source_file" "$INSTALL_PATH"
echo 'File installed'
