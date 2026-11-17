# Regenerates the PDF fixtures from cas_sample.txt (needs Docker):
#   MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD/backend/testdata:/out" -v "$PWD/backend/testdata:/s" alpine sh /s/gen_pdf_fixtures.sh
set -e
apk add --no-cache qpdf >/dev/null
cd /tmp
y=800
: > content.txt
while IFS= read -r line; do
  [ -z "$line" ] && continue
  esc=$(printf '%s' "$line" | sed -e 's|\\|\\\\|g' -e 's|(|\\(|g' -e 's|)|\\)|g')
  printf 'BT /F1 9 Tf 40 %s Td (%s) Tj ET\n' "$y" "$esc" >> content.txt
  y=$((y-14))
done < /out/cas_sample.txt
len=$(wc -c < content.txt)
{
printf '%%PDF-1.4\n'
printf '1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n'
printf '2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n'
printf '3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>\nendobj\n'
printf '4 0 obj\n<< /Length %s >>\nstream\n' "$len"
cat content.txt
printf 'endstream\nendobj\n'
printf '5 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n'
printf 'trailer\n<< /Root 1 0 R /Size 6 >>\n%%%%EOF\n'
} > raw.pdf
qpdf raw.pdf /out/cas_sample.pdf || [ $? -eq 3 ]
qpdf --allow-weak-crypto --encrypt userpw ownerpw 128 --use-aes=y -- /out/cas_sample.pdf /out/cas_sample_aes128.pdf
qpdf --allow-weak-crypto --encrypt userpw ownerpw 128 --use-aes=n -- /out/cas_sample.pdf /out/cas_sample_rc4.pdf
qpdf --encrypt userpw ownerpw 256 -- /out/cas_sample.pdf /out/cas_sample_aes256.pdf
ls -la /out/*.pdf
