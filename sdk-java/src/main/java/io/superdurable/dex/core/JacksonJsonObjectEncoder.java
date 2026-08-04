/*
 * Copyright (C) 2022 Temporal Technologies, Inc. All Rights Reserved.
 *
 * Copyright (C) 2012-2016 Amazon.com, Inc. or its affiliates. All Rights Reserved.
 *
 * Modifications copyright (C) 2017 Uber Technologies, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this material except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

/*
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications after the Legacy Cutoff are licensed under the
 * Super Durable Source License 1.0.
 * Legacy Materials remain under their original licenses.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.core;

import com.fasterxml.jackson.annotation.JsonAutoDetect;
import com.fasterxml.jackson.annotation.PropertyAccessor;
import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.DeserializationFeature;
import com.fasterxml.jackson.databind.JavaType;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.SerializationFeature;
import com.fasterxml.jackson.datatype.jdk8.Jdk8Module;
import com.fasterxml.jackson.datatype.jsr310.JavaTimeModule;
import io.superdurable.dex.gen.models.EncodedObject;

import java.io.IOException;

public class JacksonJsonObjectEncoder implements ObjectEncoder {

  private final ObjectMapper mapper;
  private final String encodingType;

  public JacksonJsonObjectEncoder() {
    mapper = new ObjectMapper();
    // preserve the original value of timezone coming from the server in Payload
    // without adjusting to the host timezone
    // may be important if the replay is happening on a host in another timezone
    mapper.configure(DeserializationFeature.ADJUST_DATES_TO_CONTEXT_TIME_ZONE, false);
    mapper.configure(SerializationFeature.WRITE_DATES_AS_TIMESTAMPS, false);
    mapper.configure(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES, false);
    mapper.registerModule(new JavaTimeModule());
    mapper.registerModule(new Jdk8Module());
    mapper.setVisibility(PropertyAccessor.FIELD, JsonAutoDetect.Visibility.ANY);
    encodingType = "json";
  }

  public JacksonJsonObjectEncoder(ObjectMapper mapper, String encodingType) {
    this.mapper = mapper;
    this.encodingType = encodingType;
  }

  @Override
  public String getEncodingType() {
    return encodingType;
  }

  @Override
  public EncodedObject encode(Object object) {
    if (object == null) {
      return null;
    }

    try {
      String data = mapper.writeValueAsString(object);
      return new EncodedObject()
              .encoding(getEncodingType())
              .data(data);
    } catch (JsonProcessingException e) {
      throw new ObjectEncoderException(e);
    }
  }

  @Override
  public <T> T decode(EncodedObject encodedObject, Class<T> type) {
    if (encodedObject == null) {
      return null;
    }

    String data = encodedObject.getData();
    if (data == null || data.isEmpty()) {
      return null;
    }

    try {
      @SuppressWarnings("deprecation")
      JavaType reference = mapper.getTypeFactory().constructType(type, type);
      return mapper.readValue(data, reference);
    } catch (IOException e) {
      throw new ObjectEncoderException(e);
    }
  }

  @Override
  public ObjectMapper getObjectMapper() {
    return this.mapper;
  }
}
