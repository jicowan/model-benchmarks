package database

// Previous ExtractModelFamily helper derived the family from the HF
// model ID via substring matching on a hardcoded list. That helper was
// replaced by Model.ModelType (sourced from HF config.json model_type)
// in the PRD-47 follow-up rename, so there's nothing left to unit test
// here — the population path is exercised via SetModelType in the
// recommend handler.
