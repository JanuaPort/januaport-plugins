# Unveränderte Kernreferenzen für L1

Quelle: öffentliches Repository `januaport`, Commit
`f646d06aefe30e5c153b6a0c15ddfcfac9313351`, Verzeichnis
`internal/desktop/testdata/`. Die Dateien wurden als Bytes kopiert, nicht neu
serialisiert. Tests lesen nur diese Spiegelung; das Kern-Repository bleibt
unverändert. Keine Abhängigkeit vom lokalen Pfad der Quelle.

| Datei | SHA-256 |
|---|---|
| contract3_routes.json | 3f09b8bfb7cb924d30c3a88211b8803ec22377d614fea04d798910eef554738a |
| manifest_setup_canonical.bin | f3060fd0110d6fd0e862e02122baef3c14c62c091f67ef25d270d5db8f60279f |
| manifest_grace_canonical.bin | 015f352e7bb53f6f2cd852469a27bcf44e0dc07df8a2e57373dc91a789d319f6 |
| manifest_test_canonical.bin | a78dca63bf2254eeae457159543e444dece6b6b4c9bc996274cf2a04492502c4 |

Das Routen-Golden enthält Vertrag 3 und symbolische Antwortwerte (`EXPIRY`,
`RUN-TOKEN`). Der Fake-Kern übernimmt die Routen/Körper, setzt den tatsächlichen
Zufalls-Nonce und eine gültige Ablaufzeit ein; Header und Halt-Körper werden
ausdrücklich gegen den Windows-Vertrag (ab K6b-1: 5) geprüft. Das Testmanifest ist absichtlich Vertrag 1
und belegt #1298; die Regression prüft zusätzlich v2, v3, v4 und v5.

`TestGoldenV3AusKern` pinnt sämtliche SHA-Werte, prüft Manifest-Roundtrips über
den HTTP-Adapter und schützt außerdem die alten v1/v2-Bytes. `contract/`,
`golden/` und `golden-v2/` werden nicht geändert.
