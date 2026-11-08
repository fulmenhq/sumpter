# 98 Negative NaN Numeric Field

Shows that unguarded XPath `number(Amount) * 10` on non-numeric text
(`<Amount>TBD</Amount>`) evaluates to IEEE 754 `NaN`, fails record serialization
(`unsupported value: NaN`), and publishes no output files.
