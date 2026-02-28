-- Seed instance types for all supported AWS accelerated instances.

INSERT INTO instance_types (name, family, accelerator_type, accelerator_name, accelerator_count, accelerator_memory_gib, vcpus, memory_gib) VALUES
  -- G5 family (NVIDIA A10G, 24 GiB each)
  ('g5.xlarge',    'g5', 'gpu', 'A10G',  1,  24,   4,   16),
  ('g5.2xlarge',   'g5', 'gpu', 'A10G',  1,  24,   8,   32),
  ('g5.4xlarge',   'g5', 'gpu', 'A10G',  1,  24,  16,   64),
  ('g5.12xlarge',  'g5', 'gpu', 'A10G',  4,  96,  48,  192),
  ('g5.48xlarge',  'g5', 'gpu', 'A10G',  8, 192,  96,  768),

  -- G6 family (NVIDIA L4, 24 GiB each)
  ('g6.xlarge',    'g6', 'gpu', 'L4',    1,  24,   4,   16),
  ('g6.2xlarge',   'g6', 'gpu', 'L4',    1,  24,   8,   32),
  ('g6.4xlarge',   'g6', 'gpu', 'L4',    1,  24,  16,   64),
  ('g6.12xlarge',  'g6', 'gpu', 'L4',    4,  96,  48,  192),
  ('g6.48xlarge',  'g6', 'gpu', 'L4',    8, 192,  96,  768),

  -- G6e family (NVIDIA L40S, 48 GiB each)
  ('g6e.xlarge',   'g6e', 'gpu', 'L40S',  1,  48,   4,   16),
  ('g6e.2xlarge',  'g6e', 'gpu', 'L40S',  1,  48,   8,   32),
  ('g6e.4xlarge',  'g6e', 'gpu', 'L40S',  1,  48,  16,   64),
  ('g6e.12xlarge', 'g6e', 'gpu', 'L40S',  4, 192,  48,  192),
  ('g6e.48xlarge', 'g6e', 'gpu', 'L40S',  8, 384,  96,  768),

  -- G7e family (NVIDIA RTX PRO Server 6000, 96 GiB each)
  ('g7e.2xlarge',  'g7e', 'gpu', 'RTX PRO 6000',  1,   96,   8,   64),
  ('g7e.4xlarge',  'g7e', 'gpu', 'RTX PRO 6000',  1,   96,  16,  128),
  ('g7e.8xlarge',  'g7e', 'gpu', 'RTX PRO 6000',  1,   96,  32,  256),
  ('g7e.12xlarge', 'g7e', 'gpu', 'RTX PRO 6000',  2,  192,  48,  512),
  ('g7e.24xlarge', 'g7e', 'gpu', 'RTX PRO 6000',  4,  384,  96, 1024),
  ('g7e.48xlarge', 'g7e', 'gpu', 'RTX PRO 6000',  8,  768, 192, 2048),

  -- GR6 family (NVIDIA L4, 24 GiB each — high-memory ratio)
  ('gr6.4xlarge',  'gr6', 'gpu', 'L4',  1,  24,  16,  128),
  ('gr6.8xlarge',  'gr6', 'gpu', 'L4',  1,  24,  32,  256),

  -- P4d family (NVIDIA A100, 40 GiB each)
  ('p4d.24xlarge', 'p4d', 'gpu', 'A100',  8, 320,  96, 1152),

  -- P4de family (NVIDIA A100, 80 GiB each)
  ('p4de.24xlarge', 'p4de', 'gpu', 'A100',  8,  640,  96, 1152),

  -- P5 family (NVIDIA H100, 80 GiB each)
  ('p5.48xlarge',  'p5', 'gpu', 'H100',  8, 640, 192, 2048),

  -- P5e family (NVIDIA H200, 141 GiB each)
  ('p5e.48xlarge', 'p5e', 'gpu', 'H200',  8, 1128, 192, 2048),

  -- P5en family (NVIDIA H200, 141 GiB each — enhanced networking)
  ('p5en.48xlarge', 'p5en', 'gpu', 'H200',  8, 1128, 192, 2048),

  -- P6-B200 family (NVIDIA B200, 179 GiB each)
  ('p6-b200.48xlarge', 'p6-b200', 'gpu', 'B200',  8, 1432, 192, 2048),

  -- P6-B300 family (NVIDIA B300, 192 GiB each)
  ('p6-b300.48xlarge', 'p6-b300', 'gpu', 'B300',  8, 1536, 192, 2250),

  -- Inf2 family (AWS Inferentia2, 32 GiB per NeuronCore)
  ('inf2.xlarge',   'inf2', 'neuron', 'Inferentia2',  2,   32,   4,  16),
  ('inf2.8xlarge',  'inf2', 'neuron', 'Inferentia2',  2,   32,  32, 128),
  ('inf2.24xlarge', 'inf2', 'neuron', 'Inferentia2', 12,  192,  96, 384),
  ('inf2.48xlarge', 'inf2', 'neuron', 'Inferentia2', 24,  384, 192, 768),

  -- Trn1 family (AWS Trainium, 32 GiB per NeuronCore)
  ('trn1.2xlarge',  'trn1', 'neuron', 'Trainium',   2,   32,   8,  32),
  ('trn1.32xlarge', 'trn1', 'neuron', 'Trainium',  32,  512, 128, 512),

  -- Trn1n family (AWS Trainium — enhanced networking)
  ('trn1n.32xlarge', 'trn1n', 'neuron', 'Trainium', 32, 512, 128, 512),

  -- Trn2 family (AWS Trainium2, 96 GiB per NeuronCore)
  ('trn2.48xlarge', 'trn2', 'neuron', 'Trainium2', 64, 6144, 192, 1536)

ON CONFLICT (name) DO NOTHING;
