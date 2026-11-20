-- Catalog owns the closed V1 vocabulary. No tenant-maintained conversion factors.
CREATE TYPE hive.catalog_unit AS ENUM ('pcs', 'g', 'kg', 'mL', 'L');

CREATE FUNCTION hive.catalog_quantity(quantity numeric, source_unit hive.catalog_unit, target_unit hive.catalog_unit)
RETURNS numeric LANGUAGE plpgsql IMMUTABLE STRICT PARALLEL SAFE AS $$
BEGIN
    IF quantity IN ('NaN'::numeric, 'Infinity'::numeric, '-Infinity'::numeric) THEN
        RAISE EXCEPTION 'non-finite quantity' USING ERRCODE = '22003';
    END IF;
    IF source_unit = target_unit THEN RETURN quantity; END IF;
    IF (source_unit, target_unit) IN (('kg', 'g'), ('L', 'mL')) THEN RETURN quantity * 1000; END IF;
    IF (source_unit, target_unit) IN (('g', 'kg'), ('mL', 'L')) THEN
        -- PostgreSQL numeric permits at most 16383 fractional digits.
        IF scale(trim_scale(quantity)) > 16380 THEN
            RAISE EXCEPTION 'converted quantity exceeds numeric scale' USING ERRCODE = '22003';
        END IF;
        RETURN quantity * 0.001;
    END IF;
    RAISE EXCEPTION 'incompatible quantity dimensions' USING ERRCODE = '22023';
END
$$;
